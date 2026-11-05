package authz

// PlatformDomain is the policy domain that carries platform operations: the
// roles, grants and bindings that apply across tenants. It is also the
// identifier of a real tenant in the roster — the system tenant — and a binding
// in this domain is a policy row of that tenant. The policy subpackage refuses
// to start when the roster holds no tenant carrying this identifier.
const PlatformDomain Domain = "system"

// The two administration roles. PlatformAdminRole exists only in the platform
// domain; TenantAdminRole exists once in every tenant domain, the platform
// domain included, and is that tenant's highest permission.
const (
	PlatformAdminRole Role = "platform-admin"
	TenantAdminRole   Role = "tenant-admin"
)

// The permissions of the authentication capability: one constant per
// authn.Action, carrying the same string. This alignment is the contract
// between the two capabilities — the action the authentication side asks about
// and the permission this side stores are one vocabulary — and a test holds it.
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

// The permissions of the tenant administration surface: the tenant-scoped role,
// grant and binding routes read and write, and the tenant settings route reads
// and writes. They carry their own codes, translated to their own (object,
// action) pairs, so a platform permission and a tenant permission are granted
// and decided apart: holding one does not pass the other's decision.
const (
	PermissionTenantRoleRead      Permission = "authz.tenant.role.read"
	PermissionTenantRoleManage    Permission = "authz.tenant.role.manage"
	PermissionTenantSettingsRead  Permission = "settings.tenant.read"
	PermissionTenantSettingsWrite Permission = "settings.tenant.write"
)

// The permissions of the tenant profile surface: reading and writing the
// organization profile a tenant carries. They are tenant-domain codes, told
// apart from the platform roster's tenant.read and tenant.manage, so a platform
// grant and a tenant grant decide separately. Both are governed: a tenant
// administrator holds them by default, while a personal tenant's administrator
// allowlist is the host's explicit list and does not include them.
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
