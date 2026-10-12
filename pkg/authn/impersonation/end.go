package impersonation

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
)

// End ends the unended record on actor's session, with the reason explicit.
//
// actor may be the impersonating identity or the actor's own; the record is
// found on the session either way, and the end is written whether or not the
// record is still valid. The ending is idempotent: a conditional update decides
// the winner, and a caller that finds the record already ended does nothing.
// The audit row and the ended event are best-effort: ending is a move in the
// safe direction, so a failed audit write is an Error and never stops it. End
// touches no carrier: the handler clears the session marker and rotates the
// cookie, and the path that revokes an impersonation token has no cookie at all.
func (m *product) End(ctx context.Context, actor authn.Identity, reason string) error {
	session := actor.Session()
	if session == "" {
		return authn.ErrNotImpersonating
	}
	g, found, err := m.store.unendedForSession(ctx, session)
	if err != nil {
		return fmt.Errorf("%w: %s", authn.ErrUnavailable, "reading the impersonation of a session")
	}
	if !found {
		return authn.ErrNotImpersonating
	}

	now := m.clock.Now()
	endedBy := actor.Key()
	won, err := m.store.end(ctx, g.ID, endedBy, authn.EndExplicit, now)
	if err != nil {
		return fmt.Errorf("%w: %s", authn.ErrUnavailable, "ending an impersonation record")
	}
	if !won {
		return nil
	}
	g.EndedAt = now
	g.EndedBy = endedBy
	g.EndReason = authn.EndExplicit
	m.endTrail(ctx, g, actionEnd, reason, "")
	m.recordSession(ctx, outcomeEnded)
	m.publishEnded(ctx, g)
	return nil
}

// Revoke ends record id by force, with the reason revoked; by is who revoked.
//
// It is idempotent: a record that is not there is ErrNotImpersonating, and one
// already ended is a no-op. It asks no Decider, because the administration
// handler has already authorized the call. The audit row and the ended event
// are best-effort.
func (m *product) Revoke(ctx context.Context, id string, by authn.Identity, reason string) error {
	g, found, err := m.store.find(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: %s", authn.ErrUnavailable, "reading an impersonation record")
	}
	if !found {
		return authn.ErrNotImpersonating
	}

	now := m.clock.Now()
	endedBy := by.Key()
	won, err := m.store.end(ctx, id, endedBy, authn.EndRevoked, now)
	if err != nil {
		return fmt.Errorf("%w: %s", authn.ErrUnavailable, "revoking an impersonation record")
	}
	if !won {
		return nil
	}
	g.EndedAt = now
	g.EndedBy = endedBy
	g.EndReason = authn.EndRevoked
	m.endTrail(ctx, g, actionRevoke, reason, endedBy)
	m.recordSession(ctx, outcomeEnded)
	m.publishEnded(ctx, g)
	return nil
}

// endTrail writes the best-effort audit row of an explicit end or a revoke,
// keyed by the action. It is the two calls End and Revoke share and nothing
// else: neither publishes nor counts, so each method does those on its own.
func (m *product) endTrail(ctx context.Context, g authn.ImpersonationGrant, action, reason, by string) {
	detail := map[string]string{detailGrantID: g.ID, detailTenant: g.Tenant, detailSID: string(g.SessionID)}
	if by != "" {
		detail[detailBy] = by
	}
	m.writeAudit(ctx, audit.Entry{
		Action:  action,
		Object:  string(g.Target),
		Outcome: outcomeSuccess,
		Purpose: reason,
		Detail:  detail,
	})
}

// endStartFailed ends a record whose carrier the handler could not attach, with
// the reason start-failed, through a conditional update and not through End. No
// event is published: the started event was never sent, so there is none to
// undo.
func (m *product) endStartFailed(ctx context.Context, id string) error {
	_, err := m.store.end(ctx, id, "", authn.EndStartFailed, m.clock.Now())
	if err != nil {
		return fmt.Errorf("%w: %s", authn.ErrUnavailable, "compensating a start whose carrier failed")
	}
	return nil
}

// List returns one page of the records the query selects, keyed by cursor: the
// second result is the cursor of the next page, empty when there is none.
func (m *product) List(ctx context.Context, q authn.GrantQuery) ([]authn.ImpersonationGrant, string, error) {
	out, next, err := m.store.list(ctx, q)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s", authn.ErrUnavailable, "listing impersonation records")
	}
	return out, next, nil
}
