package admin

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

type records interface {
	authn.Impersonation
	Get(ctx context.Context, id string) (authn.ImpersonationGrant, error)
}

func grantViewOf(g authn.ImpersonationGrant, tenantName string) AuthnAdminImpersonation {
	v := AuthnAdminImpersonation{
		ID:        g.ID,
		Actor:     string(g.Actor),
		Target:    string(g.Target),
		Tenant:    tenantName,
		SessionID: string(g.SessionID),
		StartedAt: g.StartedAt.UTC(),
		EndsAt:    g.EndsAt.UTC(),
		EndedAt:   utcPtr(g.EndedAt),
		EndReason: g.EndReason,
	}
	return v
}

func (m *product) handleImpersonationList(ctx context.Context, in impersonationListInput) (AuthnAdminImpersonationList, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return AuthnAdminImpersonationList{}, err
	}
	if err = m.admit(id, platformDomain); err != nil {
		return AuthnAdminImpersonationList{}, err
	}
	if in.Tenant == "" {
		return AuthnAdminImpersonationList{}, m.bad()
	}
	if in.Limit < 0 {
		return AuthnAdminImpersonationList{}, m.bad()
	}
	if err = m.asks(ctx, authn.Decision{Action: authn.ActionImpersonationRead, Actor: id, Tenant: in.Tenant}); err != nil {
		return AuthnAdminImpersonationList{}, err
	}
	grants, next, err := m.records.List(ctx, authn.GrantQuery{Tenant: in.Tenant, After: in.After, Limit: in.Limit})
	if err != nil {
		return AuthnAdminImpersonationList{}, m.unavailable("listing impersonation records", err)
	}
	tenantName, nameErr := m.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return AuthnAdminImpersonationList{}, m.unavailable("reading the tenant's name", nameErr)
	}
	views := make([]AuthnAdminImpersonation, 0, len(grants))
	for _, g := range grants {
		views = append(views, grantViewOf(g, tenantName))
	}
	return AuthnAdminImpersonationList{Impersonations: views, NextCursor: next}, nil
}

func (m *product) handleImpersonationRevoke(ctx context.Context, in impersonationIDInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	id, err := m.caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = m.admit(id, platformDomain); err != nil {
		return nil, err
	}
	grant, err := m.records.Get(ctx, in.ID)
	if err != nil {
		return m.replyFailure(ctx, "reading an impersonation record", err)
	}
	if grant.Tenant != in.Tenant {
		return speedhttp.EmptyReply(404), nil
	}
	if err := m.gateSignup(ctx, id); err != nil {
		return nil, err
	}
	if err := m.asks(ctx, authn.Decision{
		Action: authn.ActionImpersonationRevoke, Actor: id,
		Tenant: grant.Tenant, Target: string(grant.Target), TargetAccount: string(grant.Target),
	}); err != nil {
		return nil, err
	}
	if err := m.records.Revoke(ctx, in.ID, id, ""); err != nil {
		return m.replyFailure(ctx, "revoking an impersonation", err)
	}
	return speedhttp.EmptyReply(204), nil
}
