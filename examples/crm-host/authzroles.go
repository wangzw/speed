package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authz"
	authzguard "github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
)

// The tenant roles. A member holds one of these inside a tenant, and the role's
// grant rows are the permissions it carries. The names are the four this host
// has always used, so an existing deployment's members keep the role they had.
const (
	authzRoleOwner  authz.Role = "owner"
	authzRoleAdmin  authz.Role = "admin"
	authzRoleRep    authz.Role = "rep"
	authzRoleViewer authz.Role = "viewer"
)

// authzRoleOperator is the platform operator's role, held in the reserved
// platform domain and never in a tenant. It carries the identity system's own
// management actions and not the CRM's, so an operator manages accounts,
// sessions, factors, clients and identity providers across the platform, and
// stays a stranger to every tenant's CRM data.
const authzRoleOperator authz.Role = "operator"

// The roles in the order the provisioners write them, lowest first, so a run is
// deterministic and a case does not depend on map iteration order.
var tenantRoles = []authz.Role{authzRoleViewer, authzRoleRep, authzRoleAdmin, authzRoleOwner}

// roleDescriptionKeys is the i18n key of each role's description, keyed by role.
// A key is the host's own; the policy table stores it and the host's catalog
// renders it.
var roleDescriptionKeys = map[authz.Role]string{
	authzRoleOwner:    "crm.role.owner",
	authzRoleAdmin:    "crm.role.admin",
	authzRoleRep:      "crm.role.rep",
	authzRoleViewer:   "crm.role.viewer",
	authzRoleOperator: "crm.role.operator",
}

// The grant tiers, each one a role's permissions beyond the tier below it.
//
// The tiers replicate, permission by permission, the least role each route took
// before the routes declared permissions: a viewer reads and exports, a rep adds
// the writes (an account's owner field, a contact, a deal and an activity) and
// the deal's and the activity's deletion — the deal and activity write routes
// carry those two deletes, as they always have — an admin adds the account's and
// the contact's deletion, the pipeline's shape, the tenant's settings and its
// own audit, and an owner adds the one purge and the impersonation family. A
// permission sits in exactly one tier, so the composed role grants are
// unambiguous.
var (
	readGrants = []authz.Permission{
		permAccountRead,
		permContactRead, permContactExport,
		permDealRead,
		permPipelineRead,
		permActivityRead,
		permFlowRead,
		permReportRead,
		permMemberRead,
	}
	repGrants = []authz.Permission{
		permAccountWrite, permAccountOwn,
		permContactWrite,
		permDealWrite, permDealDel,
		permActivityWrite, permActivityDel,
	}
	adminGrants = []authz.Permission{
		permAccountDel,
		permContactDel,
		permPipelineWrite,
		permMemberManage,
		authz.PermissionSettingsRead, authz.PermissionSettingsWrite,
		authz.PermissionAuditRead,
	}
	ownerGrants = []authz.Permission{
		permAccountPurge,
		authz.PermissionImpersonateStart,
		authz.PermissionImpersonationRead,
		authz.PermissionImpersonationRevoke,
	}
)

// tenantRoleGrants is the matrix: each tenant role to the permissions it carries.
// Each tier is granted the ones below it as well, so the sets are cumulative.
var tenantRoleGrants = map[authz.Role][]authz.Permission{
	authzRoleViewer: readGrants,
	authzRoleRep:    concatPermissions(readGrants, repGrants),
	authzRoleAdmin:  concatPermissions(readGrants, repGrants, adminGrants),
	authzRoleOwner:  concatPermissions(readGrants, repGrants, adminGrants, ownerGrants),
}

// platformOperatorGrants is the platform operator's role to the identity
// system's management actions it carries.
//
// It is the management actions an operator takes that decide in the platform
// domain — accounts, sessions, factors, clients, identity providers, the
// authorization policy's own roles, and the carried surface an operator
// administers in the platform domain (the global and per-tenant settings and
// the audit trail read across tenants). An account action with no tenant
// decides in the platform domain; the policy, settings and audit routes pin it
// there through the management surface's explicit-domain form.
//
// It deliberately holds neither `authn.impersonation.start` (an operator holds
// no tenant session to start one from, so the seam refuses it), nor
// `authn.tenant.access` (an operator stays a stranger to every tenant, answered
// the same 404 as a non-member), nor `authn.membership.manage`,
// `authn.impersonation.read` and `authn.impersonation.revoke`: those three
// decide in the tenant domain (the identity system asks them with the route's
// tenant), and the operator holds no binding in any tenant, so a platform-domain
// grant of one of them would never match. A host that wants the operator to
// administer a tenant's memberships writes them through its own operator-checked
// route (the admin endpoint's `/tenants/:id/members`), not through the identity
// system's tenant-scoped management routes.
var platformOperatorGrants = []authz.Permission{
	authz.PermissionAccountRead, authz.PermissionAccountCreate,
	authz.PermissionAccountUpdate, authz.PermissionAccountDisable, authz.PermissionAccountDelete,
	authz.PermissionSessionRead, authz.PermissionSessionRevoke,
	authz.PermissionMFAReset,
	authz.PermissionClientManage,
	authz.PermissionIDPManage,
	authz.PermissionRoleRead, authz.PermissionRoleManage,
	authz.PermissionSettingsRead, authz.PermissionSettingsWrite,
	authz.PermissionAuditRead,
}

// concatPermissions joins grant tiers into one set, copying so that a role's
// slice never aliases a tier another caller might append to.
func concatPermissions(groups ...[]authz.Permission) []authz.Permission {
	var out []authz.Permission
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// resourceActionOf resolves a permission to the (object, action) pair the grant
// table stores and reports whether it is expressible. A business permission
// resolves through this host's own declared rows; a carried permission resolves
// through the capability's built-in rows. A permission neither holds has no row,
// and a caller writing it stops rather than storing an empty pair.
func resourceActionOf(perm authz.Permission) (object, action string, ok bool) {
	if declaredObject, declaredAction, found := businessObjectAction(perm); found {
		return declaredObject, declaredAction, true
	}
	return authzguard.ObjectAction(perm)
}

// tenantRolesProvisioned reports whether every tenant role is already present in
// a tenant's domain. It is what keeps a grant from re-issuing the whole matrix
// on every request: the provisioner is idempotent, but each of its rows is a
// manager write that reloads the whole cross-tenant policy and publishes a
// change event, so a grant re-provisions only when a role is missing — the first
// grant in a tenant, or one after an administrator deleted a role — and writes
// its one binding otherwise.
func tenantRolesProvisioned(ctx context.Context, mgr authz.Manager, tenant string) (bool, error) {
	roles, err := mgr.Roles(ctx, tenant)
	if err != nil {
		return false, err
	}
	held := make(map[authz.Role]bool, len(roles))
	for _, r := range roles {
		held[r.Role] = true
	}
	for _, role := range tenantRoles {
		if !held[role] {
			return false, nil
		}
	}
	return true, nil
}

// provisionTenantRoles writes the four tenant roles and their grant rows into
// tenant's domain, through the manager. It is idempotent: a role or a grant
// already there is rewritten to the same value, so a second run adds no row and
// changes none. It writes no binding — the account that holds a role is the
// caller's to name, since the manager binds an account, not a role's shape.
//
// It is the one place the tenant role matrix is written, so the grant path, the
// one-shot import and any administrative repair all call it rather than each
// writing its own copy of the permissions.
func provisionTenantRoles(ctx context.Context, mgr authz.Manager, tenant string) error {
	for _, role := range tenantRoles {
		if err := provisionRole(ctx, mgr, tenant, role, tenantRoleGrants[role]); err != nil {
			return err
		}
	}
	return nil
}

// provisionPlatformRoles writes the platform operator's role, its grant rows and
// the operator's binding, in the reserved platform domain.
//
// The operator's role is builtin, so the management surface refuses to delete it
// or change its grants: it is the role the platform's own administration is
// carried by, and losing it would lock that administration out. A grant cannot
// be written to a role that is already builtin, so the role is written editable
// and granted first, then marked builtin; a re-run reads it as builtin already
// and leaves the role and its grants as they are, re-asserting only the binding.
// Two replicas that provision at once both write the editable role and its
// grants, and whichever marks it builtin first makes the other's writes refuse
// with ErrBuiltinRole — the same end state — so that refusal is accepted. The
// run is idempotent: every row it writes is keyed by what it names, so a replica
// that restarts — or a second replica — adds no role, grant or binding.
//
// The operator is the account the static credential table answers for the
// operator subject — the full `static.<subject>` identifier, and not the static
// prefix alone — so the platform domain carries that account's role and no
// other's. The binding is written here and not in a migration: policy rows are
// data the host writes, and a migration seeds none.
func provisionPlatformRoles(ctx context.Context, mgr authz.Manager) error {
	domain := string(authz.PlatformDomain)
	locked, err := platformOperatorLocked(ctx, mgr, domain)
	if err != nil {
		return err
	}
	if !locked {
		if err := provisionRole(ctx, mgr, domain, authzRoleOperator, platformOperatorGrants); err != nil && !errors.Is(err, authz.ErrBuiltinRole) {
			return err
		}
		flip := mgr.PutRole(ctx, authz.RoleRecord{
			Tenant:         domain,
			Role:           authzRoleOperator,
			Builtin:        true,
			DescriptionKey: roleDescriptionKeys[authzRoleOperator],
		})
		if flip != nil && !errors.Is(flip, authz.ErrBuiltinRole) {
			return fmt.Errorf("provisioning the platform operator: marking the operator role builtin: %w", flip)
		}
	}
	if err := mgr.PutBinding(ctx, authz.Binding{
		Tenant: domain, Account: string(operatorAccount), Role: authzRoleOperator,
	}); err != nil {
		return fmt.Errorf("provisioning the platform operator's binding: %w", err)
	}
	return nil
}

// platformOperatorLocked reports whether the platform operator's role is builtin
// already in the platform domain. A role that is builtin is fixed — its grants
// cannot be rewritten — so a run that finds it leaves it and its grants as they
// are.
func platformOperatorLocked(ctx context.Context, mgr authz.Manager, domain string) (bool, error) {
	roles, err := mgr.Roles(ctx, domain)
	if err != nil {
		return false, fmt.Errorf("provisioning the platform operator: reading the platform domain's roles: %w", err)
	}
	for _, r := range roles {
		if r.Role == authzRoleOperator && r.Builtin {
			return true, nil
		}
	}
	return false, nil
}

// provisionRole writes one role's record and its grant rows into a domain,
// through the manager. It writes the role editable, since a grant cannot be
// added to a role that is already builtin; a caller that wants the role builtin
// marks it so after the grants are written. A permission with no translation row
// stops it: storing a grant for a permission the engine cannot express would
// write a row that never matches, so the run fails instead.
func provisionRole(ctx context.Context, mgr authz.Manager, tenant string, role authz.Role, perms []authz.Permission) error {
	if err := mgr.PutRole(ctx, authz.RoleRecord{
		Tenant:         tenant,
		Role:           role,
		Builtin:        false,
		DescriptionKey: roleDescriptionKeys[role],
	}); err != nil {
		return fmt.Errorf("provisioning the %s role in %q: %w", role, tenant, err)
	}
	for _, perm := range perms {
		object, action, ok := resourceActionOf(perm)
		if !ok {
			return fmt.Errorf("provisioning the %s role in %q: the permission %q has no translation row",
				role, tenant, perm)
		}
		if err := mgr.PutGrant(ctx, authz.GrantRecord{
			Tenant: tenant, Role: role, Resource: object, Action: action,
		}); err != nil {
			return fmt.Errorf("provisioning the %s role in %q: %w", role, tenant, err)
		}
	}
	return nil
}

// authzRolesModuleName is the name the platform-role provisioner is registered
// under. It binds no route and owns no table: it writes the platform operator's
// role, its grants and its binding at serve startup, so a served command has the
// platform's own policy in place before it accepts any request.
const authzRolesModuleName = "authz-roles"

// authzRolesProduct provisions the platform operator when a served command
// starts. It holds the policy manager it writes through.
type authzRolesProduct struct {
	released
	mgr authz.Manager
}

// start writes the platform operator's role, its grants and its binding.
//
// It is idempotent: every row it writes is keyed by what it names, so a replica
// that restarts — or a second replica — rewrites the same rows to the same
// values and adds none. The binding is the platform operator's, held in the
// reserved platform domain; the design leaves it to the host and not to a
// migration, which builds the tables and seeds no policy row. A served command
// states it here, before the first request, so the operator's administration of
// the platform's settings and audit — which decides in the platform domain —
// has a policy to decide against.
func (p *authzRolesProduct) start(ctx context.Context) error {
	if err := provisionPlatformRoles(ctx, p.mgr); err != nil {
		return fmt.Errorf("%s: provisioning the platform operator: %w", authzRolesModuleName, err)
	}
	return nil
}

// authzRolesModule binds the platform-role provisioner.
//
// It runs on serve alone: the platform's policy is what a served command decides
// against, and a command that only builds tables has no request to authorize. It
// requires the policy manager, since the write is the manager's, and delivers
// nothing of its own, so it arrives in a command only by being named — which is
// how the command tree places it.
func authzRolesModule() core.Module {
	return core.Module{
		Name: authzRolesModuleName,
		Requires: []core.Requirement{
			{Token: (*log.Logger)(nil)},
			{Token: (*authz.Manager)(nil)},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			logger, err := namedLogger(reg, authzRolesModuleName)
			if err != nil {
				return nil, err
			}
			mgr, err := core.Resolve[authz.Manager](reg)
			if err != nil {
				return nil, err
			}
			return &authzRolesProduct{released: released{log: logger}, mgr: mgr}, nil
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			product, ok := instance.(*authzRolesProduct)
			if !ok {
				return errForeignInstance(authzRolesModuleName, instance)
			}
			return product.start(ctx)
		},

		Stop:  recordStop,
		Close: recordClose,
	}
}

// init registers the platform-role provisioner. It runs on serve, the command
// whose platform administration routes decide against the policy it writes.
func init() {
	registerApplication(application{module: authzRolesModule(), runsOn: []string{serveCommand}})
}
