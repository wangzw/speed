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

// TestTheGovernedPermissionsCarryTheirGovernanceMetadata holds the catalog's
// metadata for the closed governance set: the group each permission belongs to
// and whether granting it is dangerous. The capability fills this in
// declaredPermissions, so a permission that quietly loses its mark would make
// the management surface show a dangerous grant as ordinary.
func TestTheGovernedPermissionsCarryTheirGovernanceMetadata(t *testing.T) {
	reg := core.New()
	reg.Register(Module())
	catalog := mustBuildCatalog(t, reg)

	want := map[authz.Permission]authz.PermissionMeta{
		authz.PermissionTenantRoleRead:      {Group: "authorization"},
		authz.PermissionTenantRoleManage:    {Group: "authorization", Dangerous: true},
		authz.PermissionMembershipManage:    {Group: "membership", Dangerous: true},
		authz.PermissionIDPManage:           {Group: "identity", Dangerous: true},
		authz.PermissionTenantSettingsRead:  {Group: "settings"},
		authz.PermissionTenantSettingsWrite: {Group: "settings", Dangerous: true},
		authz.PermissionTenantProfileRead:   {Group: "profile"},
		authz.PermissionTenantProfileWrite:  {Group: "profile"},
	}
	governed := catalog.TenantGoverned()
	if len(governed) != len(want) {
		t.Fatalf("the catalog governs %d permissions, the case describes %d", len(governed), len(want))
	}
	for _, permission := range governed {
		meta, ok := catalog.Metadata(permission)
		if !ok {
			t.Errorf("Metadata(%q) reports the permission undeclared", permission)
			continue
		}
		expected := want[permission]
		if meta.Group != expected.Group {
			t.Errorf("Metadata(%q).Group = %q, want %q", permission, meta.Group, expected.Group)
		}
		if meta.Dangerous != expected.Dangerous {
			t.Errorf("Metadata(%q).Dangerous = %v, want %v", permission, meta.Dangerous, expected.Dangerous)
		}
	}

	// A declared permission outside the governed set carries no metadata: the
	// zero value, and the second result true because the permission is declared.
	if meta, ok := catalog.Metadata(authz.PermissionRoleRead); !ok || meta != (authz.PermissionMeta{}) {
		t.Errorf("Metadata(platform role read) = %+v, %v, want the zero metadata and declared", meta, ok)
	}
	// An undeclared permission answers false.
	if _, ok := catalog.Metadata("crm.undeclared"); ok {
		t.Error("an undeclared permission answers metadata")
	}
}

// TestTheCatalogUnionsAModulesDeclaredMetadata holds the other half of the
// metadata contract: a module declaring a permission of its own declares its
// metadata beside it, and the catalog carries it through the union.
func TestTheCatalogUnionsAModulesDeclaredMetadata(t *testing.T) {
	reg := core.New()
	reg.Register(core.Module{
		Name: "stand-in.metadata",
		Resources: []any{authz.PermissionSpec{
			Permissions: []authz.Permission{"crm.deal.manage"},
			Metadata: map[authz.Permission]authz.PermissionMeta{
				"crm.deal.manage": {Group: "deals", Dangerous: true},
			},
		}},
	})
	catalog := mustBuildCatalog(t, reg)

	meta, ok := catalog.Metadata("crm.deal.manage")
	if !ok {
		t.Fatal("Metadata(crm.deal.manage) reports the declared permission undeclared")
	}
	if want := (authz.PermissionMeta{Group: "deals", Dangerous: true}); meta != want {
		t.Errorf("Metadata(crm.deal.manage) = %+v, want %+v", meta, want)
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
