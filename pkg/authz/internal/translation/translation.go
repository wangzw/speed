// Package translation holds the permission-to-(object, action) mapping the authorization engine matches on.
package translation

import (
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

type pair struct{ object, action string }

// builtin is the set of rows this capability carries; it is never rewritten.
var builtin = map[authz.Permission]pair{
	authz.PermissionTenantAccess:        {object: "tenant", action: "access"},
	authz.PermissionAccountRead:         {object: "account", action: "read"},
	authz.PermissionAccountCreate:       {object: "account", action: "create"},
	authz.PermissionAccountUpdate:       {object: "account", action: "update"},
	authz.PermissionAccountDisable:      {object: "account", action: "disable"},
	authz.PermissionAccountDelete:       {object: "account", action: "delete"},
	authz.PermissionMembershipManage:    {object: "membership", action: "manage"},
	authz.PermissionSessionRead:         {object: "session", action: "read"},
	authz.PermissionSessionRevoke:       {object: "session", action: "revoke"},
	authz.PermissionMFAReset:            {object: "factor", action: "reset"},
	authz.PermissionClientManage:        {object: "client", action: "manage"},
	authz.PermissionIDPManage:           {object: "idp", action: "manage"},
	authz.PermissionImpersonateStart:    {object: "impersonation", action: "start"},
	authz.PermissionImpersonationRead:   {object: "impersonation", action: "read"},
	authz.PermissionImpersonationRevoke: {object: "impersonation", action: "revoke"},
	authz.PermissionRoleRead:            {object: "role", action: "read"},
	authz.PermissionRoleManage:          {object: "role", action: "manage"},
	authz.PermissionTenantRoleRead:      {object: "tenant_role", action: "read"},
	authz.PermissionTenantRoleManage:    {object: "tenant_role", action: "manage"},
	authz.PermissionTenantSettingsRead:  {object: "tenant_settings", action: "read"},
	authz.PermissionTenantSettingsWrite: {object: "tenant_settings", action: "write"},
	authz.PermissionTenantProfileRead:   {object: "tenant_profile", action: "read"},
	authz.PermissionTenantProfileWrite:  {object: "tenant_profile", action: "write"},
	authz.PermissionAuditRead:           {object: "audit", action: "read"},
	authz.PermissionAuditExport:         {object: "audit", action: "export"},
	authz.PermissionSettingsRead:        {object: "settings", action: "read"},
	authz.PermissionSettingsWrite:       {object: "settings", action: "write"},
}

var process atomic.Pointer[map[authz.Permission]pair]

// Lookup returns the (object, action) pair a permission translates to, and
// whether the translation has a row for it at all. Before any assembly has
// resolved the process set, the built-in rows are the whole set.
func Lookup(permission authz.Permission) (object, action string, ok bool) {
	rows := builtin
	if resolved := process.Load(); resolved != nil {
		rows = *resolved
	}
	p, ok := rows[permission]
	return p.object, p.action, ok
}

// Builtin lists the permissions this capability carries a row for, in name
// order, without the rows a module declared.
func Builtin() []authz.Permission {
	out := make([]authz.Permission, 0, len(builtin))
	for permission := range builtin {
		out = append(out, permission)
	}
	slices.Sort(out)
	return out
}

// Resolve builds the process's translation set — the built-in rows unioned with
// every row the registered modules declare — and installs it.
//
// A declared row is refused, wrapped around authz.ErrUnknownPermission, when
// its object or action is empty, when it translates an already-translated
// permission to another pair, or when its (object, action) pair already names a
// different permission: the engine matches the pair alone, so a pair must be
// unique across the whole set. A row that agrees with one already there is kept
// once. A failed resolution installs nothing.
func Resolve(reg *core.Registry) error {
	rows := make(map[authz.Permission]pair, len(builtin))
	owners := make(map[pair]authz.Permission, len(builtin))
	for permission, row := range builtin {
		rows[permission] = row
		owners[row] = permission
	}
	for _, resource := range core.Resources[authz.TranslationSpec](reg) {
		for permission, declared := range resource.Value.Rows {
			row := pair{object: declared.Object, action: declared.Action}
			if row.object == "" || row.action == "" {
				return fmt.Errorf("%w: %s declares %q with an empty object or action (%q, %q)",
					authz.ErrUnknownPermission, resource.Module, permission, row.object, row.action)
			}
			if existing, ok := rows[permission]; ok {
				if existing != row {
					return fmt.Errorf("%w: %s translates %q as (%q, %q), but it is already (%q, %q)",
						authz.ErrUnknownPermission, resource.Module, permission,
						row.object, row.action, existing.object, existing.action)
				}
				continue
			}
			if other, ok := owners[row]; ok {
				return fmt.Errorf("%w: %s translates %q as (%q, %q), a pair already carried by %q: "+
					"the engine matches the pair and not the permission, so the two would be one",
					authz.ErrUnknownPermission, resource.Module, permission, row.object, row.action, other)
			}
			rows[permission] = row
			owners[row] = permission
		}
	}
	process.Store(&rows)
	return nil
}

// Reset drops the resolved set, leaving the built-in rows as the whole
// translation.
func Reset() { process.Store(nil) }
