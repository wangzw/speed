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

const (
	maxReasonRunes = 500
	hardTTLCeiling = time.Hour
)

func (m *product) Start(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest) (authn.ImpersonationGrant, error) {
	now := m.clock.Now()

	if err := m.checkActor(ctx, actor); err != nil {
		if !quietRefusal(err) {
			m.refuse(ctx, actor, r, err)
		}
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

	// The action identifier is minted after the decision and before the audit,
	// linking granted and failed rows.
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

func (m *product) checkActor(ctx context.Context, actor authn.Identity) error {
	if err := m.checkSignup(ctx, actor); err != nil {
		return err
	}
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

var errSignupUnavailable = errors.New("authn.impersonation: the signup state could not be read")

func quietRefusal(err error) bool {
	return errors.Is(err, authn.ErrContactRequired) || errors.Is(err, errSignupUnavailable)
}

func (m *product) checkSignup(ctx context.Context, actor authn.Identity) error {
	initiator := actor.Actor()
	if initiator == "" || initiator.IsStatic() {
		return nil
	}
	a, err := m.accounts.Get(ctx, initiator)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return nil
	case err != nil:
		return authn.ErrUnavailable
	}
	if a.Kind != authn.KindUser {
		return nil
	}
	status, err := m.verification.SignupStateOf(ctx, initiator)
	if err != nil {
		return fmt.Errorf("%w: %w", errSignupUnavailable, authn.ErrUnavailable)
	}
	if status.State == authn.SignupPending {
		return authn.ErrContactRequired
	}
	return nil
}

func hasStrongSecondMethod(methods []authn.Method) bool {
	for _, method := range methods {
		if method == authn.MethodTOTP || method == authn.MethodWebAuthn {
			return true
		}
	}
	return false
}

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

// checkConcurrency judges the session's unended record without writing: a lapsed
// one is left for the record transaction, so nothing is written before the
// mandatory audit.
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

func (m *product) mandatoryAudit(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest, ttl time.Duration, grantID string) error {
	if m.audit == nil {
		return authn.ErrAuditRequired
	}
	tenantName, nameErr := m.nameOf(ctx, r.Tenant)
	if nameErr != nil {
		return nameErr
	}
	err := m.audit.Record(ctx, audit.Entry{
		Action:  actionStart,
		Object:  audit.AccountObject(string(r.Target)),
		Outcome: outcomeGranted,
		Purpose: strings.TrimSpace(r.Reason),
		Detail:  startDetail(actor, r, grantID, ttl, tenantName),
	})
	if err != nil {
		return authn.ErrAuditRequired
	}
	return nil
}

// startDetail writes the session as sid, never session, which the journal owns on
// an impersonating row.
func startDetail(actor authn.Identity, r authn.ImpersonationRequest, grantID string, ttl time.Duration, tenantName string) map[string]string {
	return map[string]string{
		detailTenant:     tenantName,
		detailTTLSeconds: strconv.FormatInt(int64(ttl/time.Second), 10),
		detailSID:        string(actor.Session()),
		detailGrantID:    grantID,
	}
}

func (m *product) failedAudit(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest, grantID string) {
	tenantName, _ := m.nameOf(ctx, r.Tenant)
	m.writeAudit(ctx, audit.Entry{
		Action:  actionStart,
		Object:  audit.AccountObject(string(r.Target)),
		Outcome: outcomeFailed,
		Purpose: strings.TrimSpace(r.Reason),
		Detail:  map[string]string{detailGrantID: grantID, detailTenant: tenantName, detailSID: string(actor.Session())},
	})
}

func (m *product) refuse(ctx context.Context, actor authn.Identity, r authn.ImpersonationRequest, _ error) {
	m.recordSession(ctx, outcomeDenied)
	tenantName, _ := m.nameOf(ctx, r.Tenant)
	m.writeAudit(ctx, audit.Entry{
		Action:  actionStart,
		Object:  audit.AccountObject(string(r.Target)),
		Outcome: outcomeDenied,
		Purpose: strings.TrimSpace(r.Reason),
		Detail:  map[string]string{detailTenant: tenantName, detailSID: string(actor.Session())},
	})
}

// record writes the record in a transaction that writes only this module's table:
// it calls neither audit.Record nor Bus.Publish, which do not join it.
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

func validTenant(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}

func validReason(reason string) bool {
	if reason == "" || utf8.RuneCountInString(reason) > maxReasonRunes {
		return false
	}
	return strings.IndexFunc(reason, unicode.IsControl) < 0
}
