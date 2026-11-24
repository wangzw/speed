package impersonation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

func (m *product) Get(ctx context.Context, id string) (authn.ImpersonationGrant, error) {
	g, found, err := m.store.find(ctx, id)
	if err != nil {
		return authn.ImpersonationGrant{}, fmt.Errorf("%w: %s", authn.ErrUnavailable, "reading an impersonation record")
	}
	if !found {
		return authn.ImpersonationGrant{}, authn.ErrNotImpersonating
	}
	return g, nil
}

func (m *product) Active(ctx context.Context, id string) (authn.ImpersonationGrant, bool, error) {
	g, found, err := m.store.find(ctx, id)
	if err != nil {
		return authn.ImpersonationGrant{}, false, err
	}
	if !found {
		return g, false, nil
	}

	settled, ended, err := m.settle(ctx, m.newJudge(false), g, m.clock.Now())
	if err != nil {
		return settled, false, err
	}
	return settled, !ended, nil
}

func (m *product) settle(ctx context.Context, j *judge, g authn.ImpersonationGrant, now time.Time) (authn.ImpersonationGrant, bool, error) {
	if !g.EndedAt.IsZero() {
		return g, true, nil
	}

	reason, lapsed, err := j.lapseReason(ctx, g, now)
	if err != nil {
		return g, false, err
	}
	if !lapsed {
		return g, false, nil
	}

	won, err := m.store.end(ctx, g.ID, "", reason, now)
	if err != nil {
		return g, false, err
	}
	if won {
		g.EndedAt = now
		g.EndReason = reason
		m.recordSession(ctx, outcomeEnded)
		m.publishEnded(ctx, g)
		return g, true, nil
	}

	persisted, found, err := m.store.find(ctx, g.ID)
	if err != nil {
		return g, true, err
	}
	if found {
		return persisted, true, nil
	}
	return g, true, nil
}

type judge struct {
	m     *product
	fresh bool

	accounts    map[authn.AccountID]accountAnswer
	memberships map[membershipKey]bool
}

type accountAnswer struct {
	account authn.Account
	found   bool
}

type membershipKey struct {
	account authn.AccountID
	tenant  string
}

func (m *product) newJudge(fresh bool) *judge {
	return &judge{m: m, fresh: fresh}
}

func (j *judge) lapseReason(ctx context.Context, g authn.ImpersonationGrant, now time.Time) (reason string, lapsed bool, err error) {
	if !g.EndsAt.After(now) {
		return authn.EndExpired, true, nil
	}

	status, err := j.sessionStatus(ctx, g.SessionID)
	switch {
	case errors.Is(err, authn.ErrSessionNotFound):
		return authn.EndSessionEnded, true, nil
	case err != nil:
		return "", false, err
	}
	if !status.Active {
		return authn.EndSessionEnded, true, nil
	}

	actor, found, err := j.account(ctx, g.Actor)
	if err != nil {
		return "", false, err
	}
	if !found || actor.Status != authn.StatusActive {
		return authn.EndActorDisabled, true, nil
	}

	target, found, err := j.account(ctx, g.Target)
	if err != nil {
		return "", false, err
	}
	if !found || target.Status != authn.StatusActive {
		return authn.EndTargetDisabled, true, nil
	}

	member, err := j.member(ctx, g.Target, g.Tenant)
	if err != nil {
		return "", false, err
	}
	if !member {
		return authn.EndMembershipRemoved, true, nil
	}
	return "", false, nil
}

func (j *judge) sessionStatus(ctx context.Context, id authn.SessionID) (authn.SessionStatus, error) {
	if j.fresh {
		return j.m.sessions.StatusFresh(ctx, id)
	}
	return j.m.sessions.Status(ctx, id)
}

func (j *judge) account(ctx context.Context, id authn.AccountID) (authn.Account, bool, error) {
	if answer, ok := j.accounts[id]; ok {
		return answer.account, answer.found, nil
	}
	acct, err := j.m.accounts.Get(ctx, id)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		j.remember(id, accountAnswer{})
		return authn.Account{}, false, nil
	case err != nil:
		return authn.Account{}, false, err
	}
	j.remember(id, accountAnswer{account: acct, found: true})
	return acct, true, nil
}

func (j *judge) remember(id authn.AccountID, answer accountAnswer) {
	if j.accounts == nil {
		j.accounts = map[authn.AccountID]accountAnswer{}
	}
	j.accounts[id] = answer
}

func (j *judge) member(ctx context.Context, id authn.AccountID, tenant string) (bool, error) {
	key := membershipKey{account: id, tenant: tenant}
	if member, ok := j.memberships[key]; ok {
		return member, nil
	}
	member, err := j.m.memberships.IsMember(ctx, id, tenant)
	if err != nil {
		return false, err
	}
	if j.memberships == nil {
		j.memberships = map[membershipKey]bool{}
	}
	j.memberships[key] = member
	return member, nil
}
