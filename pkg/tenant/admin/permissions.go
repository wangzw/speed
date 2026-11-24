package admin

import "github.com/vislake/speed/pkg/authz"

const (
	// PermissionTenantRead is required to list or read tenants.
	PermissionTenantRead authz.Permission = "tenant.read"
	// PermissionTenantManage is required to register a tenant or change its name or status.
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
