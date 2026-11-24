package admin

import "github.com/vislake/speed/pkg/authz"

// The permission engine matches on an (object, action) pair, never on the
// permission key, so each permission needs a translation row; without one the
// guard denies every caller.
const (
	// PermissionTenantRead is what a caller needs to read the roster: list every
	// tenant, or read one back.
	PermissionTenantRead authz.Permission = "tenant.read"
	// PermissionTenantManage is what a caller needs to register a tenant, change
	// one's name, or change one's status.
	PermissionTenantManage authz.Permission = "tenant.manage"
)

const tenantObject = "tenant"

func permissionSpec() authz.PermissionSpec {
	return authz.PermissionSpec{
		Permissions: []authz.Permission{PermissionTenantRead, PermissionTenantManage},
		DescriptionKeys: map[authz.Permission]string{
			PermissionTenantRead:   "tenant.permission.read",
			PermissionTenantManage: "tenant.permission.manage",
		},
	}
}

func translationSpec() authz.TranslationSpec {
	return authz.TranslationSpec{Rows: map[authz.Permission]authz.Translation{
		PermissionTenantRead:   {Object: tenantObject, Action: "read"},
		PermissionTenantManage: {Object: tenantObject, Action: "manage"},
	}}
}
