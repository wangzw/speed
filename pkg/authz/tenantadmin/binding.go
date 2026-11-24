package tenantadmin

import (
	"context"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authz"
)

const (
	defaultRoleBindingPageSize = 50
	maxRoleBindingPageSize     = 200
)

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
	if err := refuseBindingRole(role); err != nil {
		return struct{}{}, err
	}
	if removingOwnBinding(ctx, role, in.Account) {
		return struct{}{}, protected()
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
