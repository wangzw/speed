package admin

import "github.com/vislake/speed/pkg/authz"

const (
	// PermissionTenantRead is what a caller needs to read the roster.
	PermissionTenantRead authz.Permission = "tenant.read"
	// PermissionTenantManage is what a caller needs to write the roster: register a tenant,
	// change its profile or status, transfer its ownership or delete it.
	PermissionTenantManage authz.Permission = "tenant.manage"
)

// The engine matches the (object, action) pair, never the permission key, so the pair must be unique across the process.
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
