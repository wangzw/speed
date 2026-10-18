package impersonation

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// The parameters a start's reason is held to, and the ceiling no setting may
// raise the lifetime past.
const (
	maxReasonRunes = 500
	hardTTLCeiling = time.Hour
)

// Start begins an impersonation on actor's session.
//
// The structural restrictions are checked first, group by group and in order
// within a group: the actor's facts, the target's, the parameters, then the
// session's concurrency. A violation is refused with the sentinel its nature
// carries and a best-effort denied audit row is written. Only then is the
// Decider asked, and only after it allows is the mandatory seam-level audit
// written, outside any transaction and before any record. The record is written
// in a transaction that first ends the session's lapsed records and then
// inserts the new one; a unique-constraint conflict there is ErrImpersonationActive
// and any other failure is ErrUnavailable, with a failed audit row written in
// either case.
//
// Start does not attach the carrier, issue a token or publish the started event:
// it has no response writer, so the handler does all three once Start returns.
func (m *product) Start(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest) (authn.ImpersonationGrant, error) {
	now := m.clock.Now()

	if err := m.checkActor(ctx, actor); err != nil {
		m.refuse(ctx, actor, r, err)
		return authn.ImpersonationGrant{}, err
	}
	if err := m.checkTarget(ctx, actor, r); err != nil {
		m.refuse(ctx, actor, r, err)
		return authn.ImpersonationGrant{}, err
	}
	ttl, err := m.checkParams(ctx, actor, r)
	if err != nil {
		m.refuse(ctx, actor, r, err)
		return authn.ImpersonationGrant{}, err
	}
	if err = m.checkConcurrency(ctx, actor, now); err != nil {
		m.refuse(ctx, actor, r, err)
		return authn.ImpersonationGrant{}, err
	}

	allow, err := m.decide(ctx, actor, r, ttl)
	if err != nil {
		return authn.ImpersonationGrant{}, err
	}
	if !allow {
		m.refuse(ctx, actor, r, authn.ErrForbidden)
		return authn.ImpersonationGrant{}, authn.ErrForbidden
	}

	// The action identifier is minted after the decision and before the audit, so
	// that the granted row and any failed row written beside it are linked.
	grantID := event.NewID()
	if err = m.mandatoryAudit(ctx, actor, r, ttl, grantID); err != nil {
		return authn.ImpersonationGrant{}, err
	}

	g, err := m.record(ctx, actor, r, ttl, grantID, now)
	if err != nil {
		m.failedAudit(ctx, actor, r, grantID)
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return authn.ImpersonationGrant{}, authn.ErrImpersonationActive
		}
		return authn.ImpersonationGrant{}, authn.ErrUnavailable
	}
	m.recordSession(ctx, outcomeStarted)
	return g, nil
}

// checkActor holds the actor to group a: not already impersonating, a human
// account that is enabled, a session to hang the record on, a local multi-factor
// authentication that is recent and not by SMS alone, and a seasoned non-SMS
// factor. The assurance, freshness, Local and method-set conditions are a
// step-up, and only a stronger authentication can clear them; the seasoned
// factor is a separate refusal, because no authentication clears it. A missing
// account is read as the actor being unavailable, which is the same refusal as
// a static or disabled one and says nothing about the target.
func (m *product) checkActor(ctx context.Context, actor authn.Identity) error {
	if actor.Impersonating() {
		return authn.ErrImpersonationRestricted
	}
	account := actor.Account()
	if account == "" || account.IsStatic() {
		return authn.ErrImpersonationRestricted
	}
	a, err := m.accounts.Get(ctx, account)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return authn.ErrImpersonationRestricted
	case err != nil:
		return authn.ErrUnavailable
	}
	if a.Kind != authn.KindUser || a.Status != authn.StatusActive {
		return authn.ErrImpersonationRestricted
	}
	if actor.Session() == "" {
		return authn.ErrImpersonationNeedsSession
	}
	req := authn.Requirement{Assurance: authn.AssuranceMulti, MaxAge: m.settings.maxAuthAge(), Local: true}
	if !actor.Satisfies(req, m.clock.Now()) {
		return authn.ErrStepUpRequired
	}
	if !hasStrongSecondMethod(actor.Methods()) {
		return authn.ErrStepUpRequired
	}
	seasoned, err := m.mfa.Seasoned(ctx, account)
	if err != nil {
		return authn.ErrUnavailable
	}
	if !seasoned {
		return authn.ErrFactorNotSeasoned
	}
	return nil
}

// hasStrongSecondMethod reports whether the method set carries a second factor
// that is not SMS: a one-time code or a passkey. A multi-factor authentication
// reached with SMS alone does not count for an impersonation, because SMS is a
// restricted authenticator.
func hasStrongSecondMethod(methods []authn.Method) bool {
	for _, method := range methods {
		if method == authn.MethodTOTP || method == authn.MethodWebAuthn {
			return true
		}
	}
	return false
}

// checkTarget holds the target to group b: an enabled human account that is not
// the actor, not a static subject, not protected, and an active member of the
// tenant the impersonation is confined to. The membership is read from the store
// and never from a cache.
func (m *product) checkTarget(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest) error {
	if r.Target == "" || r.Target == actor.Account() || r.Target.IsStatic() {
		return authn.ErrImpersonationRestricted
	}
	target, err := m.accounts.Get(ctx, r.Target)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return authn.ErrImpersonationRestricted
	case err != nil:
		return authn.ErrUnavailable
	}
	if target.Kind != authn.KindUser || target.Status != authn.StatusActive || target.Protected {
		return authn.ErrImpersonationRestricted
	}
	member, err := m.memberships.IsMember(ctx, r.Target, r.Tenant)
	if err != nil {
		return authn.ErrUnavailable
	}
	if !member {
		return authn.ErrImpersonationRestricted
	}
	return nil
}

// checkParams holds the parameters to group c and returns the settled lifetime:
// the shortest of the requested one (zero taking the setting), the max-ttl
// setting, the actor's session's remaining lifetime and the hard hour ceiling.
// A session that is not there or no longer valid leaves no lifetime to bound the
// record by, and the start is refused with ErrImpersonationNeedsSession.
func (m *product) checkParams(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest) (time.Duration, error) {
	if !validTenant(r.Tenant) {
		return 0, authn.ErrImpersonationRestricted
	}
	if reason := strings.TrimSpace(r.Reason); !validReason(reason) {
		return 0, authn.ErrImpersonationRestricted
	}

	maxTTL := m.settings.maxTTL()
	ttl := r.TTL
	if ttl <= 0 || ttl > maxTTL {
		ttl = maxTTL
	}
	status, err := m.sessions.Status(ctx, actor.Session())
	switch {
	case errors.Is(err, authn.ErrSessionNotFound):
		return 0, authn.ErrImpersonationNeedsSession
	case err != nil:
		return 0, authn.ErrUnavailable
	}
	if !status.Active {
		return 0, authn.ErrImpersonationNeedsSession
	}
	remaining := status.ExpiresAt.Sub(m.clock.Now())
	if ttl > remaining {
		ttl = remaining
	}
	if ttl > hardTTLCeiling {
		ttl = hardTTLCeiling
	}
	if ttl <= 0 {
		return 0, authn.ErrImpersonationNeedsSession
	}
	return ttl, nil
}

// checkConcurrency holds the session to group d: it may carry no valid record.
// The evaluate is a read of the validity determination and writes nothing; a
// record that has lapsed is left for the record transaction to end, so that no
// write happens before the mandatory audit.
func (m *product) checkConcurrency(ctx context.Context, actor authn.Identity, now time.Time) error {
	prior, found, err := m.store.unendedForSession(ctx, actor.Session())
	if err != nil {
		return authn.ErrUnavailable
	}
	if !found {
		return nil
	}
	_, lapsed, err := m.newJudge(false).lapseReason(ctx, prior, now)
	if err != nil {
		return authn.ErrUnavailable
	}
	if !lapsed {
		return authn.ErrImpersonationActive
	}
	return nil
}

// decide asks the Decider whether the actor may start this impersonation. An
// absent Decider refuses everything; a panic is a refusal that is neither a
// permission nor a decline, so it is ErrUnavailable and no action follows.
func (m *product) decide(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest, ttl time.Duration) (bool, error) {
	if m.decider == nil {
		return false, nil
	}
	d := authn.Decision{
		Action:        authn.ActionImpersonateStart,
		Actor:         actor,
		Tenant:        r.Tenant,
		Target:        string(r.Target),
		TargetAccount: string(r.Target),
		Params: map[string]string{
			"reason": strings.TrimSpace(r.Reason),
			"ttl":    strconv.FormatInt(int64(ttl/time.Second), 10),
		},
	}
	return m.callDecider(ctx, d)
}

// callDecider calls Decide and turns a returned error and a panic alike into
// ErrUnavailable, so that a defective Decider is refused rather than taken for a
// permission or a decline, and never takes the request down with it.
func (m *product) callDecider(ctx context.Context, d authn.Decision) (allow bool, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			allow = false
			err = fmt.Errorf("%w: the Decider panicked", authn.ErrUnavailable)
		}
	}()
	allow, err = m.decider.Decide(ctx, d)
	if err != nil {
		return false, fmt.Errorf("%w: the Decider could not answer", authn.ErrUnavailable)
	}
	return allow, nil
}

// mandatoryAudit writes the granted seam-level row outside any transaction and
// before the record. A write that fails, including a module that is not
// accepting, is ErrAuditRequired and the impersonation does not begin.
func (m *product) mandatoryAudit(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest, ttl time.Duration, grantID string) error {
	if m.audit == nil {
		return authn.ErrAuditRequired
	}
	err := m.audit.Record(ctx, audit.Entry{
		Action:  actionStart,
		Object:  string(r.Target),
		Outcome: outcomeGranted,
		Purpose: strings.TrimSpace(r.Reason),
		Detail:  startDetail(actor, r, grantID, ttl),
	})
	if err != nil {
		return authn.ErrAuditRequired
	}
	return nil
}

// startDetail is the detail of a start's audit row: the tenant, the settled
// lifetime, the actor's session and the action identifier. The session is
// written as sid; the journal owns the name session on an impersonating row.
func startDetail(actor authn.Identity, r authn.ImpersonationRequest, grantID string, ttl time.Duration) map[string]string {
	return map[string]string{
		detailTenant:     r.Tenant,
		detailTTLSeconds: strconv.FormatInt(int64(ttl/time.Second), 10),
		detailSID:        string(actor.Session()),
		detailGrantID:    grantID,
	}
}

// failedAudit writes the failed row that closes a start whose record
// transaction did not commit. It links to the granted row by the action
// identifier. A failure to write it changes nothing: the record did not land.
func (m *product) failedAudit(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest, grantID string) {
	m.writeAudit(ctx, audit.Entry{
		Action:  actionStart,
		Object:  string(r.Target),
		Outcome: outcomeFailed,
		Purpose: strings.TrimSpace(r.Reason),
		Detail:  map[string]string{detailGrantID: grantID, detailTenant: r.Tenant, detailSID: string(actor.Session())},
	})
}

// refuse writes the best-effort denied audit row and counts a denied attempt.
// It changes nothing about the refusal it accompanies. The cause is not put
// into the row: the detail's key set is closed and the refusal is told by the
// outcome.
func (m *product) refuse(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest, _ error) {
	m.recordSession(ctx, outcomeDenied)
	m.writeAudit(ctx, audit.Entry{
		Action:  actionStart,
		Object:  string(r.Target),
		Outcome: outcomeDenied,
		Purpose: strings.TrimSpace(r.Reason),
		Detail:  map[string]string{detailTenant: r.Tenant, detailSID: string(actor.Session())},
	})
}

// record writes the record in a transaction that first ends the session's lapsed
// records and then inserts the new one. The transaction writes only this
// module's table: it calls neither audit.Record nor Bus.Publish. A valid record
// left on the session makes the insert break the partial unique index, which the
// caller reads as ErrImpersonationActive.
//
// The lapse the transaction writes is remembered and announced only after the
// transaction commits: the caller that wins the conditional update counts the
// end and publishes its event, while a transaction that rolls back counted and
// announced nothing.
func (m *product) record(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest, ttl time.Duration, grantID string, now time.Time) (authn.ImpersonationGrant, error) {
	g := authn.ImpersonationGrant{
		ID:        grantID,
		Actor:     actor.Account(),
		Target:    r.Target,
		Tenant:    r.Tenant,
		Reason:    strings.TrimSpace(r.Reason),
		SessionID: actor.Session(),
		StartedAt: now,
		EndsAt:    now.Add(ttl),
	}
	var settled *authn.ImpersonationGrant
	err := m.store.session(ctx).Transaction(func(tx *gorm.DB) error {
		prior, found, err := m.store.unendedForSessionTx(tx, actor.Session())
		if err != nil {
			return err
		}
		if found {
			reason, lapsed, err := m.newJudge(false).lapseReason(ctx, prior, now)
			if err != nil {
				return err
			}
			if lapsed {
				won, err := m.store.endTx(tx, prior.ID, "", reason, now)
				if err != nil {
					return err
				}
				if won {
					prior.EndedAt = now
					prior.EndReason = reason
					settled = &prior
				}
			}
		}
		return m.store.insert(tx, g)
	})
	if err != nil {
		return authn.ImpersonationGrant{}, err
	}
	if settled != nil {
		m.recordSession(ctx, outcomeEnded)
		m.publishEnded(ctx, *settled)
	}
	return g, nil
}

// validTenant reports whether s is a tenant an impersonation may be confined to:
// non-empty and free of blanks and control characters. The rule is restated here
// and not imported, because the authentication directory does not import the
// tenant directory.
func validTenant(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}

// validReason reports whether reason is one to 500 characters with no control
// character.
func validReason(reason string) bool {
	if reason == "" || utf8.RuneCountInString(reason) > maxReasonRunes {
		return false
	}
	return strings.IndexFunc(reason, unicode.IsControl) < 0
}
