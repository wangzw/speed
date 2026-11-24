package admin

import (
	"context"
	"net/http"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func (p *product) handleBindingList(ctx context.Context, in tenantInput) (bindingListBody, error) {
	bindings, err := p.manager.Bindings(ctx, in.Tenant)
	if err != nil {
		return bindingListBody{}, p.failureOf("listing the bindings", err)
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return bindingListBody{}, p.failureOf("reading the tenant's name", nameErr)
	}
	return bindingListBody{Bindings: bindingViewsOf(bindings, tenantName)}, nil
}

func (p *product) handleBindingOf(ctx context.Context, in tenantAccountInput) (accountBindingListBody, error) {
	bindings, err := p.manager.BindingsOf(ctx, in.Tenant, in.Account)
	if err != nil {
		return accountBindingListBody{}, p.failureOf("listing the account's bindings", err)
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return accountBindingListBody{}, p.failureOf("reading the tenant's name", nameErr)
	}
	return accountBindingListBody{Bindings: bindingViewsOf(bindings, tenantName)}, nil
}

func bindingViewsOf(bindings []authz.Binding, tenantName string) []AuthzAdminBindingView {
	views := make([]AuthzAdminBindingView, len(bindings))
	for i, b := range bindings {
		views[i] = bindingViewOf(b, tenantName)
	}
	return views
}

func (p *product) handleBindingPut(ctx context.Context, in bindingInput) (struct{}, error) {
	if in.Account == "" {
		return struct{}{}, badRequest()
	}
	role := authz.Role(in.Role)
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteBinding); err != nil {
		return struct{}{}, err
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return struct{}{}, p.failureOf("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionBindingPut, audit.AccountObject(in.Account), map[string]string{
		detailActionID: actionID,
		detailTenant:   tenantName,
		detailAccount:  in.Account,
		detailRole:     string(role),
	})); err != nil {
		return struct{}{}, err
	}
	binding := authz.Binding{Tenant: in.Tenant, Account: in.Account, Role: role}
	if err := p.manager.PutBinding(ctx, binding); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionBindingPut, audit.AccountObject(in.Account), actionID))
		return struct{}{}, p.failureOf("writing a binding", err)
	}
	return struct{}{}, nil
}

func (p *product) handleBindingDelete(ctx context.Context, in bindingInput) (struct{}, error) {
	if in.Account == "" {
		return struct{}{}, badRequest()
	}
	role := authz.Role(in.Role)
	if role == authz.TenantAdminRole && removingOwnBinding(ctx, in.Account) {
		return struct{}{}, speedhttp.Fault{Status: http.StatusForbidden, Type: typePrefix + protectedCode}
	}
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteBinding); err != nil {
		return struct{}{}, err
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return struct{}{}, p.failureOf("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionBindingDelete, audit.AccountObject(in.Account), map[string]string{
		detailActionID: actionID,
		detailTenant:   tenantName,
		detailAccount:  in.Account,
		detailRole:     string(role),
	})); err != nil {
		return struct{}{}, err
	}
	if err := p.manager.DeleteBinding(ctx, in.Tenant, in.Account, role); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionBindingDelete, audit.AccountObject(in.Account), actionID))
		return struct{}{}, p.failureOf("deleting a binding", err)
	}
	return struct{}{}, nil
}

func removingOwnBinding(ctx context.Context, account string) bool {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return false
	}
	return string(id.Account()) == account
}
