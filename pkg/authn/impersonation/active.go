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
// A record read as lapsed while still unended is written back with a conditional
// update; the caller that changes the row publishes the ended event and counts
// the end, and a caller that finds the row already changed by another replica
// does neither.
func (m *product) Active(ctx context.Context, id string) (authn.ImpersonationGrant, bool, error) {
	g, found, err := m.store.find(ctx, id)
	if err != nil {
		return authn.ImpersonationGrant{}, false, err
	}
	if !found || !g.EndedAt.IsZero() {
		return g, false, nil
	}

	now := m.clock.Now()
	reason, lapsed, err := m.lapseReason(ctx, g, now)
	if err != nil {
		return g, false, err
	}
	if !lapsed {
		return g, true, nil
	}

	won, err := m.store.end(ctx, id, "", reason, now)
	if err != nil {
		return g, false, err
	}
	if won {
		g.EndedAt = now
		g.EndReason = reason
		m.recordSession(ctx, outcomeEnded)
		m.publishEnded(ctx, g)
	}
	return g, false, nil
}

// lapseReason evaluates the validity conditions from the second on and returns
// the end reason of the first that fails. lapsed is false when every condition
// holds, so the record is still valid. An error is a dependency that could not
// answer, which the caller reads as a failure and never as valid.
func (m *product) lapseReason(ctx context.Context, g authn.ImpersonationGrant, now time.Time) (reason string, lapsed bool, err error) {
	if !g.EndsAt.After(now) {
		return authn.EndExpired, true, nil
	}

	status, err := m.sessions.Status(ctx, g.SessionID)
	switch {
	case errors.Is(err, authn.ErrSessionNotFound):
		return authn.EndSessionEnded, true, nil
	case err != nil:
		return "", false, err
	}
	if !status.Active {
		return authn.EndSessionEnded, true, nil
	}

	actor, err := m.accounts.Get(ctx, g.Actor)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return authn.EndActorDisabled, true, nil
	case err != nil:
		return "", false, err
	}
	if actor.Status != authn.StatusActive {
		return authn.EndActorDisabled, true, nil
	}

	target, err := m.accounts.Get(ctx, g.Target)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return authn.EndTargetDisabled, true, nil
	case err != nil:
		return "", false, err
	}
	if target.Status != authn.StatusActive {
		return authn.EndTargetDisabled, true, nil
	}

	member, err := m.memberships.IsMember(ctx, g.Target, g.Tenant)
	if err != nil {
		return "", false, err
	}
	if !member {
		return authn.EndMembershipRemoved, true, nil
	}
	return "", false, nil
}
