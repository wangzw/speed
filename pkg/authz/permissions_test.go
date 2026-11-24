package authz

import "testing"

// TestPermissionConstantsHoldTheirPublishedValues checks that permission constants hold their published values.
func TestPermissionConstantsHoldTheirPublishedValues(t *testing.T) {
	permissions := []struct {
		name string
		got  Permission
		want string
	}{
		{"PermissionAccountRead", PermissionAccountRead, "authn.account.read"},
		{"PermissionAccountCreate", PermissionAccountCreate, "authn.account.create"},
		{"PermissionAccountUpdate", PermissionAccountUpdate, "authn.account.update"},
		{"PermissionAccountDisable", PermissionAccountDisable, "authn.account.disable"},
		{"PermissionAccountDelete", PermissionAccountDelete, "authn.account.delete"},
		{"PermissionMembershipManage", PermissionMembershipManage, "authn.membership.manage"},
		{"PermissionSessionRead", PermissionSessionRead, "authn.session.read"},
		{"PermissionSessionRevoke", PermissionSessionRevoke, "authn.session.revoke"},
		{"PermissionMFAReset", PermissionMFAReset, "authn.mfa.reset"},
		{"PermissionClientManage", PermissionClientManage, "authn.client.manage"},
		{"PermissionIDPManage", PermissionIDPManage, "authn.idp.manage"},
		{"PermissionImpersonateStart", PermissionImpersonateStart, "authn.impersonation.start"},
		{"PermissionImpersonationRead", PermissionImpersonationRead, "authn.impersonation.read"},
		{"PermissionImpersonationRevoke", PermissionImpersonationRevoke, "authn.impersonation.revoke"},
		{"PermissionTenantAccess", PermissionTenantAccess, "authn.tenant.access"},
		{"PermissionRoleRead", PermissionRoleRead, "authz.role.read"},
		{"PermissionRoleManage", PermissionRoleManage, "authz.role.manage"},
		{"PermissionTenantRoleRead", PermissionTenantRoleRead, "authz.tenant.role.read"},
		{"PermissionTenantRoleManage", PermissionTenantRoleManage, "authz.tenant.role.manage"},
		{"PermissionAuditRead", PermissionAuditRead, "audit.read"},
		{"PermissionAuditExport", PermissionAuditExport, "audit.export"},
		{"PermissionSettingsRead", PermissionSettingsRead, "settings.read"},
		{"PermissionSettingsWrite", PermissionSettingsWrite, "settings.write"},
		{"PermissionTenantSettingsRead", PermissionTenantSettingsRead, "settings.tenant.read"},
		{"PermissionTenantSettingsWrite", PermissionTenantSettingsWrite, "settings.tenant.write"},
		{"PermissionTenantProfileRead", PermissionTenantProfileRead, "tenant.profile.read"},
		{"PermissionTenantProfileWrite", PermissionTenantProfileWrite, "tenant.profile.write"},
	}
	for _, c := range permissions {
		if string(c.got) != c.want {
			t.Errorf("%s is %q, want %q", c.name, c.got, c.want)
		}
	}

	seen := map[string]string{}
	for _, c := range permissions {
		if other, dup := seen[string(c.got)]; dup {
			t.Errorf("%s and %s publish the same string %q", c.name, other, c.got)
		}
		seen[string(c.got)] = c.name
	}

	if PlatformDomain != Domain("system") {
		t.Errorf("PlatformDomain is %q, want %q", PlatformDomain, "system")
	}
}

// TestAdministrationRoleNamesHoldTheirPublishedValues checks that administration role names hold their published values.
func TestAdministrationRoleNamesHoldTheirPublishedValues(t *testing.T) {
	if PlatformAdminRole != Role("platform-admin") {
		t.Errorf("PlatformAdminRole is %q, want %q", PlatformAdminRole, "platform-admin")
	}
	if TenantAdminRole != Role("tenant-admin") {
		t.Errorf("TenantAdminRole is %q, want %q", TenantAdminRole, "tenant-admin")
	}
	if PlatformAdminRole == TenantAdminRole {
		t.Errorf("both administration roles are named %q, so a binding cannot tell them apart", PlatformAdminRole)
	}
}
