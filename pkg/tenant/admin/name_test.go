package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/tenant"
)

// TestTheNameLayerResolvesThePathTenant holds the platform boundary: a request
// under /api/v1/system/tenants/ carries the tenant's name, the layer replaces
// it with the internal identifier, and an unknown name or the old identifier
// is the bare 404. A tombstone resolves, because the platform surface manages
// it.
func TestTheNameLayerResolvesThePathTenant(t *testing.T) {
	r := newRig(t)
	r.catalog.list = []tenant.Tenant{
		{ID: "t_acme", Name: "acme", Status: tenant.StatusActive},
		{ID: "t_gone", Name: "gone", Status: tenant.StatusDeleted},
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(r.p.tenantNameLayer().Handler)
	var bound string
	serve := func(c *gin.Context) {
		bound = c.Param("tenant")
		c.Status(http.StatusOK)
	}
	engine.GET("/api/v1/system/tenants/:tenant", serve)
	engine.GET("/api/v1/tenants/:tenant", serve)

	cases := map[string]struct {
		path   string
		status int
		bound  string
	}{
		"a name resolves and is replaced by the identifier": {"/api/v1/system/tenants/acme", http.StatusOK, "t_acme"},
		"a tombstone's name resolves":                       {"/api/v1/system/tenants/gone", http.StatusOK, "t_gone"},
		"an unknown name is a 404":                          {"/api/v1/system/tenants/ghost", http.StatusNotFound, ""},
		"the old identifier is a 404":                       {"/api/v1/system/tenants/t_acme", http.StatusNotFound, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			bound = ""
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("%s answered %d, want %d", tc.path, w.Code, tc.status)
			}
			if tc.bound != "" && bound != tc.bound {
				t.Errorf("the handler bound %q, want %q", bound, tc.bound)
			}
			if tc.status == http.StatusNotFound {
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatalf("the 404 body %q is not JSON: %v", w.Body.String(), err)
				}
				if body["status"] != float64(http.StatusNotFound) {
					t.Errorf("the 404 status body = %v", body["status"])
				}
			}
		})
	}

	t.Run("the tenant-domain surface is untouched", func(t *testing.T) {
		bound = ""
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/tenants/acme", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("the tenant-domain route answered %d, want 200", w.Code)
		}
		if bound != "acme" {
			t.Errorf("the tenant-domain route bound %q, want the segment as written", bound)
		}
	})
}

// stubKind answers a fixed kind per identifier.
type stubKind map[tenant.ID]tenant.Kind

func (s stubKind) KindOf(_ context.Context, id tenant.ID) (tenant.Kind, error) {
	if kind, ok := s[id]; ok {
		return kind, nil
	}
	return tenant.KindBusiness, nil
}

// TestTheViewCarriesTheTenantKind holds the value a client branches on: a
// personal tenant reads personal through a kind resolver, and every tenant
// reads business when the assembly supplies none.
func TestTheViewCarriesTheTenantKind(t *testing.T) {
	r := newRig(t)
	// The roster orders by name, so the fixture is handed over in that order.
	r.catalog.list = []tenant.Tenant{
		{ID: "t_acme", Name: "acme", Status: tenant.StatusActive},
		{ID: "personal-acc-1", Name: "alice", Status: tenant.StatusActive},
	}
	r.p.kind = stubKind{
		"personal-acc-1": tenant.KindPersonal,
		"t_acme":         tenant.KindBusiness,
	}

	w := r.do(http.MethodGet, pathTenants, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var got struct {
		Tenants []tenantView `json:"tenants"`
	}
	decode(t, w, &got)
	if len(got.Tenants) != 2 || got.Tenants[0].Kind != "business" || got.Tenants[1].Kind != "personal" {
		t.Fatalf("tenants = %+v, want business (acme) then personal (alice)", got.Tenants)
	}
	if got.Tenants[0].Name != "acme" || got.Tenants[1].Name != "alice" {
		t.Errorf("names = %q, %q; want the roster's order", got.Tenants[0].Name, got.Tenants[1].Name)
	}
}
