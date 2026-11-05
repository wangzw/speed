package tenantadmin

import (
	"context"

	"github.com/vislake/speed/pkg/authz"
)

// The role usage read's page size: the default a query names none, and the
// ceiling a query is held to rather than trusted.
const (
	defaultRoleBindingPageSize = 50
	maxRoleBindingPageSize     = 200
)

// handleRoleBindingList answers GET
// /api/v1/tenants/:tenant/roles/:role/bindings: one page of the accounts the
// role is bound to, with their display names, activity and grant instant. It is
// a read, so the action is authz.tenant.role.read.
//
// The cursor is the last account on the page, so a caller continues from where
// the previous page ended. One extra row is read to tell a full last page from
// one that has a successor. A role with no binding is an empty page and an
// empty cursor, not an error.
func (p *product) handleRoleBindingList(ctx context.Context, in roleBindingListInput) (roleBindingPageBody, error) {
	limit := in.Limit
	switch {
	case limit < 0:
		return roleBindingPageBody{}, badRequest()
	case limit == 0:
		limit = defaultRoleBindingPageSize
	case limit > maxRoleBindingPageSize:
		limit = maxRoleBindingPageSize
	}
	bindings, err := p.manager.BindingsOfRole(ctx, in.Tenant, authz.Role(in.Role), in.After, limit+1)
	if err != nil {
		return roleBindingPageBody{}, p.failureOf("listing the role's bindings", err)
	}
	next := ""
	if len(bindings) > limit {
		bindings = bindings[:limit]
		next = bindings[len(bindings)-1].Account
	}
	var identities map[string]authz.AccountIdentity
	if p.identities != nil && len(bindings) > 0 {
		accounts := make([]string, len(bindings))
		for i, b := range bindings {
			accounts[i] = b.Account
		}
		identities, err = p.accountIdentities(ctx, in.Tenant, accounts)
		if err != nil {
			return roleBindingPageBody{}, err
		}
	}
	return roleBindingPageBody{
		Bindings:   roleBindingViewsOf(bindings, identities),
		NextCursor: next,
	}, nil
}

// handleBindingList answers GET /api/v1/tenants/:tenant/bindings: every role
// binding of the tenant. It is a read, so the action is authz.tenant.role.read.
func (p *product) handleBindingList(ctx context.Context, in tenantInput) (bindingListBody, error) {
	bindings, err := p.manager.Bindings(ctx, in.Tenant)
	if err != nil {
		return bindingListBody{}, p.failureOf("listing the bindings", err)
	}
	return bindingListBody{Bindings: bindingViewsOf(bindings)}, nil
}

// handleBindingOf answers GET
// /api/v1/tenants/:tenant/bindings/:account: every binding the account holds
// within the tenant. It is a read, so the action is authz.tenant.role.read.
func (p *product) handleBindingOf(ctx context.Context, in tenantAccountInput) (accountBindingListBody, error) {
	bindings, err := p.manager.BindingsOf(ctx, in.Tenant, in.Account)
	if err != nil {
		return accountBindingListBody{}, p.failureOf("listing the account's bindings", err)
	}
	return accountBindingListBody{Bindings: bindingViewsOf(bindings)}, nil
}

// handleBindingPut answers PUT
// /api/v1/tenants/:tenant/bindings/:account/:role: it binds a role to an
// account within the tenant. It is a write, so the action is
// authz.tenant.role.manage.
//
// The checks run in this order, before the host write guard, the audit and the
// manager: a non-empty account, a role that is not platform-admin, the caller's
// held set, the account's membership, and the role's subset of the held set. An
// empty account is a 400 because the route addresses a binding by its account;
// platform-admin is a protected refusal because it may not be placed in a
// tenant domain; a non-member is a 400; a role higher than the caller is a
// protected refusal.
func (p *product) handleBindingPut(ctx context.Context, in bindingInput) (struct{}, error) {
	if in.Account == "" {
		return struct{}{}, badRequest()
	}
	role := authz.Role(in.Role)
	if err := refuseBindingRole(role); err != nil {
		return struct{}{}, err
	}
	held, err := p.heldPermissions(ctx, in.Tenant)
	if err != nil {
		return struct{}{}, err
	}
	if err := p.requireMembership(ctx, in.Tenant, in.Account); err != nil {
		return struct{}{}, err
	}
	if err := p.requireRoleSubset(ctx, in.Tenant, role, held); err != nil {
		return struct{}{}, err
	}
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteBinding); err != nil {
		return struct{}{}, err
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionBindingPut, in.Account, map[string]string{
		detailActionID: actionID,
		detailTenant:   in.Tenant,
		detailAccount:  in.Account,
		detailRole:     string(role),
	})); err != nil {
		return struct{}{}, err
	}
	binding := authz.Binding{Tenant: in.Tenant, Account: in.Account, Role: role}
	if err := p.manager.PutBinding(ctx, binding); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionBindingPut, in.Account, actionID))
		return struct{}{}, p.failureOf("writing a binding", err)
	}
	return struct{}{}, nil
}

// handleBindingDelete answers DELETE
// /api/v1/tenants/:tenant/bindings/:account/:role: it removes an account's
// binding to a role within the tenant. It is a write, so the action is
// authz.tenant.role.manage.
//
// A non-empty account and a role that is not platform-admin are checked first.
// A caller may not remove its own tenant-admin binding: the manager enforces
// the domain's last-administrator floor but reads no actor, so the rule that one
// administrator cannot take its own permission away is made here, with the
// protected code, before the host write guard, the audit and the manager.
func (p *product) handleBindingDelete(ctx context.Context, in bindingInput) (struct{}, error) {
	if in.Account == "" {
		return struct{}{}, badRequest()
	}
	role := authz.Role(in.Role)
	if err := refuseBindingRole(role); err != nil {
		return struct{}{}, err
	}
	if removingOwnBinding(ctx, role, in.Account) {
		return struct{}{}, protected()
	}
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteBinding); err != nil {
		return struct{}{}, err
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionBindingDelete, in.Account, map[string]string{
		detailActionID: actionID,
		detailTenant:   in.Tenant,
		detailAccount:  in.Account,
		detailRole:     string(role),
	})); err != nil {
		return struct{}{}, err
	}
	if err := p.manager.DeleteBinding(ctx, in.Tenant, in.Account, role); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionBindingDelete, in.Account, actionID))
		return struct{}{}, p.failureOf("deleting a binding", err)
	}
	return struct{}{}, nil
}
