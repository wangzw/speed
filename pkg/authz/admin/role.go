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

// handleRoleList answers GET /api/v1/system/tenants/:tenant/roles: every role of
// the tenant. The tenant in the path is the policy domain, taken as the
// identifier and not an alias; this module holds no tenant roster and does not
// look the tenant up. It is a read, so the action is role.read; the identity and
// the permission are decided by the route's guard wrapper before this handler is
// called.
//
// Each view carries the role's description, its grant count, its active member
// count and whether the caller may manage roles. The member count is read only
// when the assembly delivers an account-identity reader; a read that fails is a
// 503 and never a count of zero. The caller's held permissions are read the same
// way: a failure is a 503, never an empty set.
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
	views := make([]AuthzAdminRoleView, len(roles))
	for i, r := range roles {
		views[i] = p.roleViewOf(ctx, r, grantCounts[r.Role], memberCounts[r.Role], canManage)
	}
	return roleListBody{Roles: views}, nil
}

// errNoIdentity is what a role list reports when the request carries no
// identity. The route's guard wrapper refuses such a request before the handler
// runs, so this is unreachable through the mounted surface; a handler called
// directly is answered as an outage rather than being given an empty held set.
var errNoIdentity = speedhttp.Fault{Status: http.StatusServiceUnavailable}

// heldPermissions reads the caller's effective permissions in the platform
// domain through SubjectPermissions. The subject is the request's effective
// account — the one an authorization decision uses — and not the tenant a path
// or body names, which is the data a route acts on. The returned set is the one
// source for both the role list's can_manage and the catalog's grantable. A read
// that fails is a 503 and is never read as an empty set: an empty set is a
// conclusion, a fault is not.
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

// canManageRoles reports whether the caller holds authz.role.manage in the
// platform domain, the permission the role, grant and binding writes are held
// to. It reads the same held set the catalog's grantable uses, so the two
// fields cannot disagree. A read that fails is a 503 and is never read as
// "cannot manage": a client that hid the controls on a fault would be told the
// caller's permission set is one it could not read.
func (p *product) canManageRoles(ctx context.Context) (bool, error) {
	held, err := p.heldPermissions(ctx)
	if err != nil {
		return false, err
	}
	return held[authz.PermissionRoleManage], nil
}

// roleMemberCounts counts the active members of every role from one tenant-wide
// binding read and one batch identity read. A nil result member is the unknown
// state: with no identity reader the count cannot be told from an unavailable
// one, so it is reported as null and never as zero. An account the host does not
// know is left out of every count; the bindings it holds stay rows of the role,
// but no activity state can vouch for it.
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

// accountIdentities resolves the accounts a role-usage read names. A nil reader
// answers nil: the identity state is unknown and the caller reports it as such.
// A reader that cannot answer is a 503, never an empty or inactive set.
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

// handleRolePut answers PUT /api/v1/system/tenants/:tenant/roles/:role: it creates
// or rewrites a role without the builtin mark. It is a write, so the action is
// role.manage. The optional host WriteGuard is asked after the shape checks;
// the mandatory audit row lands before the manager is called. A builtin role
// the manager refuses is a 409 with a failed row beside the permitted one.
//
// The body carries the role's free-text description: absent leaves the stored
// text as it is, and an explicit empty string clears it. A description longer
// than authz.RoleDescriptionMax characters is a 400 before the write guard, the
// audit and the manager.
func (p *product) handleRolePut(ctx context.Context, in roleWriteInput) (struct{}, error) {
	role := authz.Role(in.Role)
	// The description is the request's to set: an explicit empty string clears
	// the free text rather than being treated as absent. The role name is not
	// writable: it comes from the path, which is the binding's key.
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > authz.RoleDescriptionMax {
		return struct{}{}, badRequest()
	}
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteRole); err != nil {
		return struct{}{}, err
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionRolePut, string(role), map[string]string{
		detailActionID: actionID,
		detailTenant:   in.Tenant,
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

// handleRoleDelete answers DELETE /api/v1/system/tenants/:tenant/roles/:role: it
// removes a role, which revokes its grants and bindings with it. It is a write,
// so the action is role.manage. The optional host WriteGuard is asked after the
// shape checks. A builtin role the manager refuses is a 409.
func (p *product) handleRoleDelete(ctx context.Context, in roleInput) (struct{}, error) {
	role := authz.Role(in.Role)
	if err := p.requireWriteAllowed(ctx, in.Tenant, authz.WriteRole); err != nil {
		return struct{}{}, err
	}
	actionID := newActionID()
	if err := p.auditRequired(ctx, mandatory(actionRoleDelete, string(role), map[string]string{
		detailActionID: actionID,
		detailTenant:   in.Tenant,
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

// roleChange names the kind of record a role write changes, for the audit row's
// change key.
const roleChange = "role"
