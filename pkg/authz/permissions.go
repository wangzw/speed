package authz

// PlatformDomain is the policy domain that carries platform operations, and the
// identifier of the system tenant in the roster.
const PlatformDomain Domain = "system"

// The two administration roles. PlatformAdminRole exists only in the platform
// domain; TenantAdminRole exists once in every tenant domain and is that
// tenant's highest permission.
const (
	PlatformAdminRole Role = "platform-admin"
	TenantAdminRole   Role = "tenant-admin"
)

// The permissions of the authentication capability: one constant per
// authn.Action, carrying the same string.
const (
	PermissionAccountRead         Permission = "authn.account.read"
	PermissionAccountCreate       Permission = "authn.account.create"
	PermissionAccountUpdate       Permission = "authn.account.update"
	PermissionAccountDisable      Permission = "authn.account.disable"
	PermissionAccountDelete       Permission = "authn.account.delete"
	PermissionMembershipManage    Permission = "authn.membership.manage"
	PermissionSessionRead         Permission = "authn.session.read"
	PermissionSessionRevoke       Permission = "authn.session.revoke"
	PermissionMFAReset            Permission = "authn.mfa.reset"
	PermissionClientManage        Permission = "authn.client.manage"
	PermissionIDPManage           Permission = "authn.idp.manage"
	PermissionImpersonateStart    Permission = "authn.impersonation.start"
	PermissionImpersonationRead   Permission = "authn.impersonation.read"
	PermissionImpersonationRevoke Permission = "authn.impersonation.revoke"
	PermissionTenantAccess        Permission = "authn.tenant.access"
)

// The permissions of this capability's own management surface.
const (
	PermissionRoleRead   Permission = "authz.role.read"
	PermissionRoleManage Permission = "authz.role.manage"
)

// The permissions of the tenant administration surface. They carry their own
// codes, translated to their own (object, action) pairs, so a platform
// permission and a tenant permission are granted and decided apart.
const (
	PermissionTenantRoleRead      Permission = "authz.tenant.role.read"
	PermissionTenantRoleManage    Permission = "authz.tenant.role.manage"
	PermissionTenantSettingsRead  Permission = "settings.tenant.read"
	PermissionTenantSettingsWrite Permission = "settings.tenant.write"
)

// The permissions of the tenant profile surface: reading and writing the
// organization profile a tenant carries. Both are governed.
const (
	PermissionTenantProfileRead  Permission = "tenant.profile.read"
	PermissionTenantProfileWrite Permission = "tenant.profile.write"
)

// The permissions this capability carries for the audit and settings
// capabilities: their search and export, and their write surface.
const (
	PermissionAuditRead     Permission = "audit.read"
	PermissionAuditExport   Permission = "audit.export"
	PermissionSettingsRead  Permission = "settings.read"
	PermissionSettingsWrite Permission = "settings.write"
)
