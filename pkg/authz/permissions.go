package authz

// PlatformDomain is the reserved policy domain that carries platform
// operations: the roles, grants and bindings that apply across tenants. No
// tenant may carry this identifier, and the policy subpackage checks the roster
// for a tenant named this at startup.
const PlatformDomain Domain = "system"

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

// The permissions this capability carries for the audit and settings
// capabilities: their search and export, and their write surface.
const (
	PermissionAuditRead     Permission = "audit.read"
	PermissionAuditExport   Permission = "audit.export"
	PermissionSettingsRead  Permission = "settings.read"
	PermissionSettingsWrite Permission = "settings.write"
)
