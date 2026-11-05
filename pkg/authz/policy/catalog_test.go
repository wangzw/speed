package policy

import (
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

// TestTheCentralCatalogSplitsTheAdministrationGrantSets holds the governed set
// the process declares: the tenant-administration codes, membership management
// and identity-provider management, and nothing else. The platform
// administrator's full declared set is not narrowed; the tenant administrator's
// exact set is this one.
func TestTheCentralCatalogSplitsTheAdministrationGrantSets(t *testing.T) {
	reg := core.New()
	reg.Register(Module())
	catalog := mustBuildCatalog(t, reg)

	want := []authz.Permission{
		authz.PermissionIDPManage,
		authz.PermissionMembershipManage,
		authz.PermissionTenantRoleManage,
		authz.PermissionTenantRoleRead,
		authz.PermissionTenantSettingsRead,
		authz.PermissionTenantSettingsWrite,
		authz.PermissionTenantProfileRead,
		authz.PermissionTenantProfileWrite,
	}
	if got := catalog.TenantGoverned(); !slices.Equal(got, want) {
		t.Errorf("TenantGoverned() = %v, want %v", got, want)
	}
	for _, permission := range want {
		if !catalog.Has(permission) {
			t.Errorf("the governed permission %q is not declared", permission)
		}
		if !catalog.Governed(permission) {
			t.Errorf("Governed(%q) = false, want true", permission)
		}
	}
	for _, permission := range []authz.Permission{
		authz.PermissionRoleRead,
		authz.PermissionRoleManage,
		authz.PermissionSettingsRead,
		authz.PermissionSettingsWrite,
		authz.PermissionAccountRead,
		authz.PermissionSessionRead,
		authz.PermissionAuditRead,
		authz.PermissionTenantAccess,
	} {
		if catalog.Governed(permission) {
			t.Errorf("Governed(%q) = true, want false: it is a platform-only permission", permission)
		}
	}
	if catalog.Governed("crm.undeclared") {
		t.Error("an undeclared permission answers governed")
	}

	// TenantGoverned answers a copy, as Declared does.
	governed := catalog.TenantGoverned()
	governed[0] = authz.Permission("mutated")
	if catalog.TenantGoverned()[0] == authz.Permission("mutated") {
		t.Error("TenantGoverned handed back the catalog's own slice")
	}
}

// TestTheCatalogTranslatesAPairBackToItsPermission holds the catalog's reverse
// read: the engine matches a grant's (object, action) pair and never sees the
// permission, so a view built from the pair names the permission a client wrote
// it as. Every declared permission with a translation row round-trips, and a
// pair no declared permission translates to answers false rather than a name.
func TestTheCatalogTranslatesAPairBackToItsPermission(t *testing.T) {
	reg := core.New()
	reg.Register(Module())
	catalog := mustBuildCatalog(t, reg)

	reverse, ok := any(catalog).(interface {
		PermissionOf(object, action string) (authz.Permission, bool)
	})
	if !ok {
		t.Fatal("the catalog exposes no PermissionOf read")
	}
	for _, permission := range catalog.Declared() {
		object, action, translated := catalog.ObjectAction(permission)
		if !translated {
			continue // a declared permission may carry no translation row
		}
		got, found := reverse.PermissionOf(object, action)
		if !found || got != permission {
			t.Errorf("PermissionOf(%q, %q) = (%q, %v), want (%q, true)",
				object, action, got, found, permission)
		}
	}
	if got, found := reverse.PermissionOf("retired_object", "retired_action"); found {
		t.Errorf("PermissionOf of an untranslated pair = (%q, true), want the zero permission and false", got)
	}
}

// TestACatalogRefusesAGovernedPermissionItDoesNotDeclare holds the one shape the
// governed metadata is checked for: a module marking a permission governed
// without listing it in its own Permissions is a startup ErrUnknownPermission.
func TestACatalogRefusesAGovernedPermissionItDoesNotDeclare(t *testing.T) {
	reg := core.New()
	reg.Register(core.Module{
		Name: "stand-in.bad-governed",
		Resources: []any{authz.PermissionSpec{
			Permissions:    []authz.Permission{"crm.deal.read"},
			TenantGoverned: []authz.Permission{"crm.deal.manage"},
		}},
	})
	_, err := buildCatalog(reg)
	if !errors.Is(err, authz.ErrUnknownPermission) {
		t.Fatalf("buildCatalog gave %v, want ErrUnknownPermission", err)
	}
}
