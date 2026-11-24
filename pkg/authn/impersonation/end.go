package impersonation

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
)

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

func (m *product) Revoke(ctx context.Context, id string, by authn.Identity, reason string) error {
	g, found, err := m.store.find(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: %s", authn.ErrUnavailable, "reading an impersonation record")
	}
	if !found {
		return authn.ErrNotImpersonating
	}

	now := m.clock.Now()
	g, ended, err := m.settle(ctx, m.newJudge(true), g, now)
	if err != nil {
		return fmt.Errorf("%w: %s", authn.ErrUnavailable, "revoking an impersonation record")
	}
	if ended {
		return nil
	}

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

func (m *product) endTrail(ctx context.Context, g authn.ImpersonationGrant, action, reason, by string) {
	tenantName, _ := m.nameOf(ctx, g.Tenant)
	detail := map[string]string{detailGrantID: g.ID, detailTenant: tenantName, detailSID: string(g.SessionID)}
	if by != "" {
		detail[detailBy] = by
	}
	m.writeAudit(ctx, audit.Entry{
		Action:  action,
		Object:  audit.AccountObject(string(g.Target)),
		Outcome: outcomeSuccess,
		Purpose: reason,
		Detail:  detail,
	})
}

func (m *product) endStartFailed(ctx context.Context, id string) error {
	_, err := m.store.end(ctx, id, "", authn.EndStartFailed, m.clock.Now())
	if err != nil {
		return fmt.Errorf("%w: %s", authn.ErrUnavailable, "compensating a start whose carrier failed")
	}
	return nil
}

func (m *product) List(ctx context.Context, q authn.GrantQuery) ([]authn.ImpersonationGrant, string, error) {
	out, next, err := m.store.list(ctx, q)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s", authn.ErrUnavailable, "listing impersonation records")
	}

	now := m.clock.Now()
	j := m.newJudge(true)
	for i := range out {
		if !out[i].EndedAt.IsZero() {
			continue
		}
		settled, _, err := m.settle(ctx, j, out[i], now)
		if err != nil {
			return nil, "", fmt.Errorf("%w: %s", authn.ErrUnavailable, "settling an impersonation record")
		}
		out[i] = settled
	}
	return out, next, nil
}
