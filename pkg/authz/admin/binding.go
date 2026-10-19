package admin

import (
	"context"

	"github.com/vislake/speed/pkg/authz"
)

// handleBindingList answers GET /api/v1/system/tenants/:tenant/bindings: every
// role binding of the tenant. It is a read, so the action is role.read; the
// identity and the permission are decided by the route's guard wrapper before
// this handler is called.
func (p *product) handleBindingList(ctx context.Context, in tenantInput) (bindingListBody, error) {
	bindings, err := p.manager.Bindings(ctx, in.Tenant)
	if err != nil {
		return bindingListBody{}, p.failureOf("listing the bindings", err)
	}
	return bindingListBody{Bindings: bindingViewsOf(bindings)}, nil
}

// handleBindingOf answers GET
// /api/v1/system/tenants/:tenant/bindings/:account: every binding the account
// holds within the tenant. It is a read, so the action is role.read.
func (p *product) handleBindingOf(ctx context.Context, in tenantAccountInput) (accountBindingListBody, error) {
	bindings, err := p.manager.BindingsOf(ctx, in.Tenant, in.Account)
	if err != nil {
		return accountBindingListBody{}, p.failureOf("listing the account's bindings", err)
	}
	return accountBindingListBody{Bindings: bindingViewsOf(bindings)}, nil
}

// bindingViewsOf builds the views of a binding read.
func bindingViewsOf(bindings []authz.Binding) []AuthzAdminBindingView {
	views := make([]AuthzAdminBindingView, len(bindings))
	for i, b := range bindings {
		views[i] = bindingViewOf(b)
	}
	return views
}

// handleBindingPut answers PUT
// /api/v1/system/tenants/:tenant/bindings/:account/:role: it binds a role to an
// account within the tenant. It is a write, so the action is role.manage. The
// mandatory audit row lands before the manager writes.
//
// An empty account is refused on shape before the manager is called: a binding
// addresses an account, and the empty account names the zero subject, so a
// request that carries none is a 400 rather than a write the manager would
// refuse without a code the module maps.
func (p *product) handleBindingPut(ctx context.Context, in bindingInput) (struct{}, error) {
	if in.Account == "" {
		return struct{}{}, badRequest()
	}
	role := authz.Role(in.Role)
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
// /api/v1/system/tenants/:tenant/bindings/:account/:role: it removes an account's
// binding to a role within the tenant. It is a write, so the action is
// role.manage. The mandatory audit row lands before the manager writes.
//
// An empty account is refused on shape before the manager is called, the same
// as the binding write: the route addresses a binding by its account, and an
// empty account names no row.
func (p *product) handleBindingDelete(ctx context.Context, in bindingInput) (struct{}, error) {
	if in.Account == "" {
		return struct{}{}, badRequest()
	}
	role := authz.Role(in.Role)
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
