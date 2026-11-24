package admin

import (
	"context"
	"log/slog"
	"net/http"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func (p *product) handleRoleList(ctx context.Context, in tenantInput) (roleListBody, error) {
	roles, err := p.manager.Roles(ctx, in.Tenant)
	if err != nil {
		return roleListBody{}, p.failureOf("listing the roles", err)
	}
	grantCounts, err := p.manager.GrantCounts(ctx, in.Tenant)
	if err != nil {
		return roleListBody{}, p.failureOf("counting the roles' grants", err)
	}
	canManage, err := p.canManageRoles(ctx)
	if err != nil {
		return roleListBody{}, err
	}
	memberCounts, err := p.roleMemberCounts(ctx, in.Tenant, roles)
	if err != nil {
		return roleListBody{}, err
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return roleListBody{}, p.failureOf("reading the tenant's name", nameErr)
	}
	views := make([]AuthzAdminRoleView, len(roles))
	for i, r := range roles {
		views[i] = p.roleViewOf(ctx, r, grantCounts[r.Role], memberCounts[r.Role], canManage, tenantName)
	}
	return roleListBody{Roles: views}, nil
}

var errNoIdentity = speedhttp.Fault{Status: http.StatusServiceUnavailable}

func (p *product) heldPermissions(ctx context.Context) (map[authz.Permission]bool, error) {
	id, ok := authn.FromContext(ctx)
	if !ok {
		p.logger.Error(ModuleName + ": the caller could not be read, so the held permission set is unavailable")
		return nil, errNoIdentity
	}
	held, err := p.subject.Held(ctx, authz.PlatformDomain, string(id.Account()))
	if err != nil {
		return nil, p.failureOf("reading the caller's permissions", err)
	}
	set := make(map[authz.Permission]bool, len(held))
	for _, permission := range held {
		set[permission] = true
	}
	return set, nil
}

func (p *product) canManageRoles(ctx context.Context) (bool, error) {
	held, err := p.heldPermissions(ctx)
	if err != nil {
		return false, err
	}
	return held[authz.PermissionRoleManage], nil
}

func (p *product) roleMemberCounts(ctx context.Context, tenant string, roles []authz.RoleRecord) (map[authz.Role]*int, error) {
	counts := make(map[authz.Role]*int, len(roles))
	if p.identities == nil {
		for _, role := range roles {
			counts[role.Role] = nil
		}
		return counts, nil
	}
	bindings, err := p.manager.Bindings(ctx, tenant)
	if err != nil {
		return nil, p.failureOf("listing the roles' bindings", err)
	}
	accountsByRole := make(map[authz.Role][]string, len(roles))
	seen := make(map[string]struct{}, len(bindings))
	accounts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		accountsByRole[b.Role] = append(accountsByRole[b.Role], b.Account)
		if _, twice := seen[b.Account]; twice {
			continue
		}
		seen[b.Account] = struct{}{}
		accounts = append(accounts, b.Account)
	}
	var identities map[string]authz.AccountIdentity
	if len(accounts) > 0 {
		identities, err = p.accountIdentities(ctx, tenant, accounts)
		if err != nil {
			return nil, err
		}
	}
	for _, role := range roles {
		active := 0
		for _, account := range accountsByRole[role.Role] {
			if identity, known := identities[account]; known && identity.Active {
				active++
			}
		}
		count := active
		counts[role.Role] = &count
	}
	return counts, nil
}

func (p *product) accountIdentities(ctx context.Context, tenant string, accounts []string) (map[string]authz.AccountIdentity, error) {
	if p.identities == nil {
		return nil, nil
	}
	identities, err := p.identities.Identities(ctx, tenant, accounts)
	if err != nil {
		p.logger.Error(ModuleName+": the accounts' identity could not be read, so the member counts are unavailable",
			slog.String("tenant", tenant), slog.String("cause", err.Error()))
		return nil, speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	return identities, nil
}

func (p *product) handleRolePut(ctx context.Context, in roleWriteInput) (struct{}, error) {
	role := authz.Role(in.Role)
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > authz.RoleDescriptionMax {
		return struct{}{}, badRequest()
	}
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteRole); err != nil {
		return struct{}{}, err
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return struct{}{}, p.failureOf("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionRolePut, string(role), map[string]string{
		detailActionID: actionID,
		detailTenant:   tenantName,
		detailRole:     string(role),
		detailChange:   roleChange,
	})); err != nil {
		return struct{}{}, err
	}
	record := authz.RoleRecord{
		Tenant: in.Tenant,
		Role:   role,
	}
	if in.Description != nil {
		record.Description = *in.Description
	}
	if err := p.manager.PutRole(ctx, record); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionRolePut, string(role), actionID))
		return struct{}{}, p.failureOf("writing a role", err)
	}
	return struct{}{}, nil
}

func (p *product) handleRoleDelete(ctx context.Context, in roleInput) (struct{}, error) {
	role := authz.Role(in.Role)
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteRole); err != nil {
		return struct{}{}, err
	}
	tenantName, nameErr := p.nameOf(ctx, in.Tenant)
	if nameErr != nil {
		return struct{}{}, p.failureOf("reading the tenant's name", nameErr)
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionRoleDelete, string(role), map[string]string{
		detailActionID: actionID,
		detailTenant:   tenantName,
		detailRole:     string(role),
	})); err != nil {
		return struct{}{}, err
	}
	if err := p.manager.DeleteRole(ctx, in.Tenant, role); err != nil {
		p.auditBestEffort(ctx, failedAudit(actionRoleDelete, string(role), actionID))
		return struct{}{}, p.failureOf("deleting a role", err)
	}
	return struct{}{}, nil
}

const roleChange = "role"
