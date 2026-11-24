package admin

import (
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authz"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/settings"
)

type policyDecision struct {
	perm   authz.Permission
	domain authz.Domain
}

type policyGuard struct {
	mu      sync.Mutex
	allowed map[policyDecision]bool
	asked   []policyDecision
}

func newPolicyGuard(allowed ...policyDecision) *policyGuard {
	g := &policyGuard{allowed: make(map[policyDecision]bool, len(allowed))}
	for _, d := range allowed {
		g.allowed[d] = true
	}
	return g
}

func (g *policyGuard) note(p authz.Permission, domain authz.Domain) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := policyDecision{perm: p, domain: domain}
	g.asked = append(g.asked, d)
	return g.allowed[d]
}

func (g *policyGuard) Allowed(_ *gin.Context, perm authz.Permission) (bool, error) {
	return g.note(perm, ""), nil
}

func (g *policyGuard) RequirePermission(c *gin.Context, perm authz.Permission) bool {
	if g.note(perm, "") {
		return true
	}
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusForbidden})
	return false
}

func (g *policyGuard) RequirePermissionIn(c *gin.Context, perm authz.Permission, domain authz.Domain) bool {
	if g.note(perm, domain) {
		return true
	}
	speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusForbidden})
	return false
}

// TestAPlatformStaffMemberWithATenantAdminBindingReachesThatTenantLayer checks
// that the tenant codes in the tenant domain are what the tenant layer asks for.
func TestAPlatformStaffMemberWithATenantAdminBindingReachesThatTenantLayer(t *testing.T) {
	declared := []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}

	t.Run("read", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = declared
		r.p.guard = newPolicyGuard(
			policyDecision{authz.PermissionTenantSettingsRead, authz.Domain("acme")},
			policyDecision{authz.PermissionTenantSettingsWrite, authz.Domain("acme")},
		)
		w := r.do(http.MethodGet, "/api/v1/system/settings?layer=tenant&tenant=acme", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("the read ended with %d, want 200: %s", w.Code, w.Body.String())
		}
	})

	t.Run("write", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = declared
		r.p.guard = newPolicyGuard(
			policyDecision{authz.PermissionTenantSettingsRead, authz.Domain("acme")},
			policyDecision{authz.PermissionTenantSettingsWrite, authz.Domain("acme")},
		)
		w := r.do(http.MethodPut, "/api/v1/system/settings/app/mode?layer=tenant&tenant=acme",
			map[string]any{"value": "manual"})
		if w.Code != http.StatusNoContent {
			t.Fatalf("the write ended with %d, want 204: %s", w.Code, w.Body.String())
		}
	})
}

// TestAPlatformAdministratorWithoutATenantBindingIsRefusedOnATenantLayer checks
// that the global codes do not stand in for the tenant codes.
func TestAPlatformAdministratorWithoutATenantBindingIsRefusedOnATenantLayer(t *testing.T) {
	declared := []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}

	for name, target := range map[string]struct {
		method string
		target string
		body   any
	}{
		"read":  {http.MethodGet, "/api/v1/system/settings?layer=tenant&tenant=acme", nil},
		"write": {http.MethodPut, "/api/v1/system/settings/app/mode?layer=tenant&tenant=acme", map[string]any{"value": "manual"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.store.declared = declared
			r.p.guard = newPolicyGuard(
				policyDecision{authz.PermissionSettingsRead, authz.PlatformDomain},
				policyDecision{authz.PermissionSettingsWrite, authz.PlatformDomain},
				policyDecision{authz.PermissionSettingsRead, authz.Domain("acme")},
				policyDecision{authz.PermissionSettingsWrite, authz.Domain("acme")},
			)
			w := r.do(target.method, target.target, target.body)
			if w.Code != http.StatusForbidden {
				t.Fatalf("the request ended with %d, want 403: %s", w.Code, w.Body.String())
			}
			if calls := r.store.recorded(); len(calls) != 0 {
				t.Errorf("the settings surface was reached (%v) without the tenant codes", calls)
			}
		})
	}
}

// TestTheGlobalLayerKeepsTheGlobalCodesAndThePlatformDomain checks that the tenant codes cannot reach the global layer.
func TestTheGlobalLayerKeepsTheGlobalCodesAndThePlatformDomain(t *testing.T) {
	declared := []settings.DeclaredItem{
		decl("app.mode", "app", reflect.TypeFor[string](), "auto", settings.Item{}),
	}

	t.Run("a platform grant reaches the global layer", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = declared
		r.p.guard = newPolicyGuard(
			policyDecision{authz.PermissionSettingsRead, authz.PlatformDomain},
			policyDecision{authz.PermissionSettingsWrite, authz.PlatformDomain},
		)
		if w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil); w.Code != http.StatusOK {
			t.Fatalf("the read ended with %d, want 200: %s", w.Code, w.Body.String())
		}
		if w := r.do(http.MethodPut, "/api/v1/system/settings/app/mode?layer=global",
			map[string]any{"value": "on"}); w.Code != http.StatusNoContent {
			t.Fatalf("the write ended with %d, want 204: %s", w.Code, w.Body.String())
		}
	})

	t.Run("a tenant grant does not reach the global layer", func(t *testing.T) {
		r := newRig(t)
		r.store.declared = declared
		r.p.guard = newPolicyGuard(
			policyDecision{authz.PermissionTenantSettingsRead, authz.Domain("acme")},
			policyDecision{authz.PermissionTenantSettingsWrite, authz.Domain("acme")},
		)
		if w := r.do(http.MethodGet, "/api/v1/system/settings?layer=global", nil); w.Code != http.StatusForbidden {
			t.Fatalf("the read ended with %d, want 403: %s", w.Code, w.Body.String())
		}
	})
}
