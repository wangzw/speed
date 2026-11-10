package admin

// The acceptance evidence for the tenant management surface: a tombstone is
// readable but refuses every write, the process permission catalog carries no
// tenant.delete code, and the two deletion surfaces decide differently.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// A tombstone reads 200 with the deleted state and refuses every write with
// 409, on both the platform and the tenant surface. The roster itself holds the
// terminal rule (pkg/tenant/catalog's TestATombstoneRefusesEveryWrite); this
// case holds that every management route maps it, and that no refused write
// reaches the organization writer.
func TestATombstoneReadsAndRefusesEveryWrite(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		r := newRig(t)
		r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusDeleted}
		r.catalog.found = true

		w := r.do(http.MethodGet, pathTenants+"/t_acme", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
		}
		var got struct {
			Tenant tenantView `json:"tenant"`
		}
		decode(t, w, &got)
		if got.Tenant.Status != "deleted" {
			t.Errorf("status = %q, want deleted", got.Tenant.Status)
		}
	})

	writes := map[string]struct {
		method   string
		target   string
		body     any
		inTenant bool
	}{
		"platform status": {
			http.MethodPost, pathTenants + "/t_acme/status", statusBody{Status: "disabled"}, false,
		},
		"platform profile": {
			http.MethodPatch, pathTenants + "/t_acme/profile", fullProfile(), false,
		},
		"platform owner transfer request": {
			http.MethodPost, pathTenants + "/t_acme/owner/requests", map[string]any{"account": "acc-2"}, false,
		},
		"platform delete": {
			http.MethodDelete, pathTenants + "/t_acme", nil, false,
		},
		"tenant owner transfer request": {
			http.MethodPost, tenantPath(pathOwnerRequests, "t_acme"), map[string]any{"account": "acc-2"}, true,
		},
		"tenant delete": {
			http.MethodDelete, tenantPath(pathOrganization, "t_acme"), nil, true,
		},
	}
	for name, tc := range writes {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			// The tenant-surface guard decides by owner identity, so the caller
			// is the row's owner and the permission guard would refuse a
			// permission it was asked for. The platform routes are allowed
			// through, so the only refusal left is the tombstone.
			r.guard.allow = !tc.inTenant
			r.catalog.held = tenant.Tenant{
				ID: "t_acme", Name: "acme", Status: tenant.StatusDeleted,
				OwnerAccount: authntest.DefaultAccount.String(),
			}
			r.catalog.found = true
			r.catalog.setStatusErr = tenant.ErrTenantDeleted
			r.catalog.setProfileErr = tenant.ErrTenantDeleted

			var rec *httptest.ResponseRecorder
			if tc.inTenant {
				rec = r.doInTenant(tc.method, tc.target, "t_acme", tc.body)
			} else {
				rec = r.do(tc.method, tc.target, tc.body)
			}
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body %s)", rec.Code, rec.Body.String())
			}
			if calls := r.organization.recorded(); len(calls) != 0 {
				t.Errorf("the organization writer was called %+v, want no call", calls)
			}
		})
	}
}

// The process permission catalog has no tenant.delete code. Deletion is decided
// by owner identity on the tenant surface and by tenant.manage on the platform
// surface, and no new permission exists for it.
func TestThePermissionCatalogHasNoTenantDelete(t *testing.T) {
	declared := map[authz.Permission]bool{}
	for _, resource := range Module().Resources {
		if spec, ok := resource.(authz.PermissionSpec); ok {
			for _, permission := range spec.Permissions {
				declared[permission] = true
			}
		}
	}

	if declared[authz.Permission("tenant.delete")] {
		t.Fatal("the permission catalog declares tenant.delete, want no such permission")
	}
	want := map[authz.Permission]bool{
		PermissionTenantRead:   true,
		PermissionTenantManage: true,
	}
	for permission := range declared {
		if !want[permission] {
			t.Errorf("the catalog declares %q, want only tenant.read and tenant.manage", permission)
		}
	}
	for permission := range want {
		if !declared[permission] {
			t.Errorf("the catalog does not declare %q", permission)
		}
	}
}

// Deletion is decided by owner identity on the tenant surface and by
// tenant.manage on the platform surface. The tenant surface asks no permission:
// the owner passes even when every permission decision would refuse, and the
// guard is never consulted.
func TestTheDeleteIsDecidedByOwnerIdentityOnTheTenantSurface(t *testing.T) {
	t.Run("tenant surface", func(t *testing.T) {
		r := newRig(t)
		r.guard.allow = false
		r.catalog.held = tenant.Tenant{
			ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
			OwnerAccount: authntest.DefaultAccount.String(),
		}
		r.catalog.found = true

		w := r.doInTenant(http.MethodDelete, tenantPath(pathOrganization, "t_acme"), "t_acme", nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
		}
		if asked := r.guard.askedFor(); len(asked) != 0 {
			t.Errorf("the tenant surface asked the permission guard %v, want no permission", asked)
		}
	})

	t.Run("platform surface", func(t *testing.T) {
		r := newRig(t)
		r.guard.allow = true
		r.catalog.held = tenant.Tenant{
			ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
			OwnerAccount: authntest.DefaultAccount.String(),
		}
		r.catalog.found = true

		w := r.do(http.MethodDelete, pathTenants+"/t_acme", nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
		}
		asked := r.guard.askedFor()
		if len(asked) != 1 || asked[0] != PermissionTenantManage {
			t.Errorf("the platform surface asked %v, want exactly [%q]", asked, PermissionTenantManage)
		}
	})
}
