package admin

import (
	"context"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// records is the part of the impersonation capability the record routes read:
// the published Impersonation interface together with reading one record by its
// identifier. The capability the impersonation module delivers answers this, and
// the record group is mounted only when it does, because the routes resolve a
// record's tenant and target before asking the Decider and cannot do so from the
// published List alone.
type records interface {
	authn.Impersonation
	Get(ctx context.Context, id string) (authn.ImpersonationGrant, error)
}

// grantViewOf builds one impersonation record as the management routes show it.
// The reason never appears; it is in the record and the audit row alone.
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

// handleImpersonationList answers GET /tenants/:tenant/impersonations: it lists
// the records of the tenant the path names. The action is impersonation.read,
// decided in the platform domain: the path's tenant is the data the records are
// confined to, and the caller needs no permission in it.
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
	// The tenant is the path segment and is passed to the store, so the page
	// holds only that tenant's records and the cursor counts them.
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

// handleImpersonationRevoke answers DELETE /tenants/:tenant/impersonations/:id: it reads
// the record to learn its tenant and target, asks the Decider with those, and
// revokes it. The action is impersonation.revoke, decided in the platform
// domain: the record's tenant is the data the record is confined to, and the
// caller needs no permission in it. It writes no mandatory audit:
// the revocation is a move in the safe direction and the impersonation module
// writes its own best-effort row, so a failed audit does not stop the
// revocation. Revoking a record that has already ended is a no-op and gets the
// same answer as the first revocation.
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
	// The path names the tenant the operation is filed under; a record that
	// belongs to another tenant answers not found, the same answer a record
	// that does not exist gets.
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
