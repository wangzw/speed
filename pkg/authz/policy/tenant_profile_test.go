package policy

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestTheTenantProfilePermissionsAreDeclaredAndGoverned checks that the tenant profile permissions are declared and governed.
func TestTheTenantProfilePermissionsAreDeclaredAndGoverned(t *testing.T) {
	spec := declaredPermissions()

	for _, permission := range []authz.Permission{
		authz.PermissionTenantProfileRead,
		authz.PermissionTenantProfileWrite,
	} {
		if !slices.Contains(spec.Permissions, permission) {
			t.Errorf("the catalog does not declare %q", permission)
		}
		if !slices.Contains(spec.TenantGoverned, permission) {
			t.Errorf("the catalog does not mark %q governed, so a tenant administrator would not hold it", permission)
		}
	}

	wantKeys := map[authz.Permission]string{
		authz.PermissionTenantProfileRead:  "authz.permission.tenant-profile-read",
		authz.PermissionTenantProfileWrite: "authz.permission.tenant-profile-write",
	}
	for permission, want := range wantKeys {
		if got := spec.DescriptionKeys[permission]; got != want {
			t.Errorf("DescriptionKeys[%q] = %q, want %q", permission, got, want)
		}
	}
}

// TestTheTenantProfileDescriptionsExistInEveryLocale checks that the tenant profile descriptions exist in every locale.
func TestTheTenantProfileDescriptionsExistInEveryLocale(t *testing.T) {
	keys := []string{
		"authz.permission.tenant-profile-read",
		"authz.permission.tenant-profile-write",
	}
	for _, locale := range []string{"zh-CN", "en-US"} {
		raw, err := os.ReadFile("locales/" + locale + ".json")
		if err != nil {
			t.Fatalf("reading the %s locale: %v", locale, err)
		}
		var messages map[string]string
		if err := json.Unmarshal(raw, &messages); err != nil {
			t.Fatalf("decoding the %s locale: %v", locale, err)
		}
		for _, key := range keys {
			if messages[key] == "" {
				t.Errorf("the %s locale carries no text for %q", locale, key)
			}
		}
	}
}
