package impersonation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// Get reads one record by its identifier, ended or not, without judging whether
// it is still in force. A record that is not there is authn.ErrNotImpersonating.
// The administration routes read a record here to learn its tenant and target
// before asking the Decider; it is not part of the published Impersonation
// interface, whose method set is fixed, and the routes take it by asserting the
// capability to their own narrow interface.
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

// Active answers whether the record id is valid now.
//
// The conditions are a closed set and the order is both the order they are
// judged in and the order a lapse is written back with. A record that is not
// there, or has already ended, is false at once. Otherwise the end time, the
// bound session, the actor's account, the target's account and the target's
// membership are checked in turn, and the first that fails names the end reason.
// The evaluation is settle's; a record read as lapsed while still unended is
// written back there with a conditional update, and the caller that changes the
// row publishes the ended event and counts the end. Active reads the bound
// session through the check-ttl cache, because it is the verification hot path;
// the management paths read the store.
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

// settle judges one record with the validity conditions and writes a lapse back
// with a conditional update. ended reports whether the record is ended when
// settle returns: it already was as it was read, or this call or another replica
// ended it. The record returned is the one as persisted; when another caller won
// the conditional update, the row is read again so that the reason that landed
// is the one returned. The caller that wins the update publishes the ended event
// and counts the end; a caller that finds the row already changed does neither.
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

	// The conditional update says another caller wrote the end first, and its
	// reason is the one that landed. Returning this evaluation's reason would
	// state a cause the row does not carry.
	persisted, found, err := m.store.find(ctx, g.ID)
	if err != nil {
		return g, true, err
	}
	if found {
		return persisted, true, nil
	}
	return g, true, nil
}

// judge evaluates the validity conditions of one record and holds the answers it
// has read for its caller. A caller that judges one record builds one for that
// record; a caller that judges a page builds one for the page, so that an
// account or a membership the rows share is read once. The bound session is read
// per record: a record has its own.
//
// fresh selects the session read. The verification hot path (Active) reads the
// check-ttl cache; the management paths (List, Revoke) read the store, because
// the cache is the upper bound of what a verifying backend believes and not of
// what an administrator is shown or forces.
type judge struct {
	m     *product
	fresh bool

	accounts    map[authn.AccountID]accountAnswer
	memberships map[membershipKey]bool
}

// accountAnswer is what the account read said: the account, and whether there
// was one.
type accountAnswer struct {
	account authn.Account
	found   bool
}

// membershipKey identifies a membership read, which a page's rows share when
// they target the same account in the same tenant.
type membershipKey struct {
	account authn.AccountID
	tenant  string
}

// newJudge builds a judge over the product, reading the bound session fresh or
// through the cache. Its answers are held lazily: a record that fails on the
// session read, which is the common case on Active's hot path, never allocates
// one.
func (m *product) newJudge(fresh bool) *judge {
	return &judge{m: m, fresh: fresh}
}

// lapseReason evaluates the validity conditions from the second on and returns
// the end reason of the first that fails. lapsed is false when every condition
// holds, so the record is still valid. An error is a dependency that could not
// answer, which the caller reads as a failure and never as valid.
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

// sessionStatus reads the bound session, from the store when the judge reads
// fresh and through the check-ttl cache otherwise.
func (j *judge) sessionStatus(ctx context.Context, id authn.SessionID) (authn.SessionStatus, error) {
	if j.fresh {
		return j.m.sessions.StatusFresh(ctx, id)
	}
	return j.m.sessions.Status(ctx, id)
}

// account reads an account, once per judge. A missing account is not an error:
// the caller reads it as the account being unavailable and names its end reason.
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

// remember holds an account answer for the rest of the judge's life.
func (j *judge) remember(id authn.AccountID, answer accountAnswer) {
	if j.accounts == nil {
		j.accounts = map[authn.AccountID]accountAnswer{}
	}
	j.accounts[id] = answer
}

// member reads a membership, once per judge and pair.
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
